package mcp

// set_context (issue #52) writes to Node-RED's context store. The admin
// API exposes no way to write context, so the MCP manages a tiny
// inject+function helper on the runtime and dispatches a payload to it
// over POST /inject/:id.
//
// Why an inject+function pair, not just an inject: Node-RED 5.x's inject
// node's input handler always ends in RED.util.setMessageProperty(msg, ...)
// — it can only set msg fields, never flow/global/context. The actual
// write has to happen in a downstream function node whose code is fixed
// and dispatches on msg.scope.
//
// Why a persistent helper, not a fresh node per call: per-call nodes
// mean a write, a deploy, and a write per set_context — noisy, slow, and
// visible in the user's flow. A one-time helper keeps the runtime
// untouched across invocations and matches the issue's "reused, not
// recreated" requirement.
//
// Concurrency: the helper is shared across all goroutines that call
// set_context concurrently. Server.ctxHelperMu guards the entire
// helper lifecycle — pointer publication, metadata writes, and the
// restore-side invalidation. The lock is held across the whole
// critical section (provisioning + inject dispatch) so the helper
// observed at the fast path is the same one the inject uses, and a
// restore cannot interleave so a caller uses a stale helper after a
// successful restore. See ensureSetContextHelper for the full
// contract and issue #310 for the original audit.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// setContextHelper holds the runtime-side ids of the helper flow the MCP
// provisions for the set_context tool. Zero value = not provisioned. The
// helper is rediscovered and verified against runtime state before each use.
//
// Once a helper is published (its flowID/injectID/functionID are set),
// it is effectively immutable: every field is written exactly once,
// under Server.ctxHelperMu, before the pointer becomes visible to other
// readers. Readers only consult a non-nil, fully-populated helper and
// never mutate it, so the fields can be loaded without re-locking.
type setContextHelper struct {
	// flowID, injectID, functionID are the Node-RED ids the helper
	// installs in the runtime. They are selected without colliding with
	// existing entries and never change after this helper is published.
	flowID     string
	injectID   string
	functionID string
	// Legacy aliases are valid only when no unrelated entry occupies that ID.
	flowAliasAllowed     bool
	functionAliasAllowed bool
}

// provisioned reports whether the helper has been installed on the
// runtime. Callers must still hold ctxHelperMu and verify runtime state.
func (h *setContextHelper) provisioned() bool {
	return h != nil && h.flowID != "" && h.injectID != ""
}

// setContextHelperIDs is the well-known identity of the helper. The
// labels are what shows in the editor; the ids are what GET /flows and
// POST /inject/:id see. They are deliberately stable across runs so
// the same Server can re-discover an already-provisioned helper after
// a restart. If those IDs are already occupied by unrelated/edited nodes,
// provisioning selects fresh IDs; discovery verifies the executable shape.
const (
	setContextHelperFlowID     = "mcp_ctx_helper_tab"
	setContextHelperFlowLabel  = "__mcp_context_helper__"
	setContextHelperInjectID   = "mcp_ctx_helper_inj"
	setContextHelperInjectName = "__mcp_context_helper_inject__"
	setContextHelperFunctionID = "mcp_ctx_helper_fn"
	setContextHelperFunctionNm = "__mcp_context_helper_set__"
)

// setContextFunctionCode is the body of the helper function node. It
// dispatches on msg.scope, which arrives as a top-level field of the
// inject's msg (Node-RED 5.x forwards the POST /inject/:id body to
// node.receive when __user_inject_props__ is present).
//
// Note: this runs inside the runtime, not inside the MCP, so the JSON
// fields are JS values, not Go values. The shape is documented in
// handleSetContext; the runtime gets the value through JSON.parse +
// JSON.stringify round-trips, so numbers stay numbers, strings stay
// strings, and objects stay objects.
const setContextFunctionCode = `const { scope, key, value } = msg;
if (typeof key !== "string" || key.length === 0) {
    node.error("set_context: key is required");
    return msg;
}
if (scope === "global") {
    global.set(key, value);
} else if (scope === "flow") {
    flow.set(key, value);
} else if (scope === "node") {
    context.set(key, value);
} else {
    node.error("set_context: scope must be global, flow or node, got " + scope);
}
return msg;
`

// setContextProvisioningDelay is how long the first call after a
// lazy-provisioned helper waits before retrying a 404 on /inject/:id.
// Node-RED's routing layer occasionally answers 404 to the first
// inject request after a freshly-deployed node before the deploy has
// fully propagated. The value is small enough that a recovery is
// imperceptible to the caller, and large enough that it covers the
// deploy-propagation window observed in the wild (issue #158).
const setContextProvisioningDelay = 200 * time.Millisecond

// isNodeNotFound reports whether err is a Node-RED 404 (the helper
// inject was not yet visible to the routing layer, or the id is
// genuinely missing). Issue #158 uses it to gate the retry on a
// just-provisioned helper.
func isNodeNotFound(err error) bool {
	var apiErr *nodered.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// injectWithProvisioningRetry fires the helper's inject and, if the
// helper was just provisioned in this call and the first attempt
// returns a Node-RED 404, retries once after setContextProvisioningDelay.
// The retry is gated on justProvisioned so a genuine "helper missing"
// failure on a subsequent call still surfaces to the operator instead
// of being masked by a retry (issue #158).
func (s *Server) injectWithProvisioningRetry(ctx context.Context, injectID string, body []byte, justProvisioned bool) error {
	err := s.nrClient.InjectNodeWithBody(ctx, injectID, body)
	if err == nil || !justProvisioned || !isNodeNotFound(err) {
		return err
	}
	time.Sleep(setContextProvisioningDelay)
	return s.nrClient.InjectNodeWithBody(ctx, injectID, body)
}

// ensureSetContextHelper returns the lazily-provisioned set_context
// helper, verifying it against the runtime on every call. It provisions a
// complete replacement only when no supported, unmodified helper is found.
//
// Locking contract (issue #310): the caller MUST hold
// Server.ctxHelperMu for the entire critical section that includes
// the inject dispatch in handleSetContext. Holding the lock here
// means:
//   - pointer publication (s.ctxHelper = h) and metadata writes
//     (h.flowID, h.injectID, h.functionID) happen under the same
//     mutex acquisition, so a reader can never observe a partially-
//     populated helper;
//   - concurrent callers on this Server discover the first caller's complete
//     deployment instead of creating duplicate tabs;
//   - a restore_backup cannot nil s.ctxHelper out from under an
//     in-flight set_context, so no caller can use a helper that the
//     restore has invalidated.
//
// The second return value (justProvisioned) is true when this call
// performed the provisioning work; it gates the post-provisioning
// retry on a 404 (issue #158).
func (s *Server) ensureSetContextHelper(ctx context.Context) (*setContextHelper, bool, error) {
	return s.ensureContextHelperTarget(ctx, "global", "")
}

// contextTargetError keeps validation results distinct from provisioning failures.
type contextTargetError string

func (e contextTargetError) Error() string { return string(e) }

func (s *Server) ensureContextHelperTarget(ctx context.Context, scope, id string) (*setContextHelper, bool, error) {
	if s.readOnly {
		// Defensive: the tool is withheld in read-only mode, so this
		// should never be reached. Surface a clear error rather than
		// silently failing in a less obvious way.
		return nil, false, fmt.Errorf("set_context is not available in read-only mode")
	}

	// Re-read the runtime, even when cached: set_flows, deletion, an editor,
	// or another MCP process can replace the helper without notifying us.
	if denied := s.deniedHelperNodeType(); denied != "" {
		return nil, false, &nodered.DeniedNodeError{NodeType: denied}
	}
	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("discovering helper: %w", err)
	}
	helper, used, err := discoverContextHelper(raw)
	if err != nil {
		return nil, false, err
	}
	if helper != nil {
		if message := validateContextTarget(scope, id, helper); message != "" {
			return nil, false, contextTargetError(message)
		}
		// Preserve identity for existing readers under ctxHelperMu.
		if s.ctxHelper == nil || *s.ctxHelper != *helper {
			s.ctxHelper = helper
		}
		return s.ctxHelper, false, nil
	}
	// Reject a foreign explicit target before creating any configuration.
	if message := validateContextTarget(scope, id, newContextHelper("", "", "", used)); message != "" {
		return nil, false, contextTargetError(message)
	}
	s.ctxHelper = nil
	helper, err = s.provisionSetContextHelper(ctx, used)
	if err != nil {
		return nil, false, err
	}
	s.ctxHelper = helper
	return helper, true, nil
}

// provisionSetContextHelper deploys a complete tab in one guarded operation.
// A rejected deployment cannot leave a half-built tab. If the response is lost,
// a later explicit call discovers the completed helper instead of replaying it.
func (s *Server) provisionSetContextHelper(ctx context.Context, used map[string]bool) (*setContextHelper, error) {
	functionID, injectID := setContextHelperFunctionID, setContextHelperInjectID
	if used[functionID] || used[injectID] {
		// Node IDs are globally scoped. Never reuse an unrelated/edited node.
		for {
			suffix := make([]byte, 8)
			if _, err := rand.Read(suffix); err != nil {
				return nil, fmt.Errorf("generating helper IDs: %w", err)
			}
			functionID = fmt.Sprintf("mcp_ctx_fn_%x", suffix)
			injectID = fmt.Sprintf("mcp_ctx_inj_%x", suffix)
			if !used[functionID] && !used[injectID] {
				break
			}
		}
	}
	tab := contextHelperDocument(functionID, injectID)
	created, err := s.nrClient.CreateFlow(ctx, tab)
	if err != nil {
		return nil, fmt.Errorf("creating helper flow tab: %w", err)
	}
	flowID, err := extractFlowID(created)
	if err != nil {
		return nil, fmt.Errorf("helper flow id missing from CreateFlow response: %w", err)
	}
	return newContextHelper(flowID, functionID, injectID, used), nil
}

// The runtime assigns the tab ID and rewrites its children's z references.
func contextHelperDocument(functionID, injectID string) nodered.RawFlow {
	return nodered.RawFlow(fmt.Sprintf(
		`{"id":%q,"label":%q,"nodes":[`+
			`{"id":%q,"type":"function","z":%q,"name":%q,"func":%q,"outputs":1,"noerr":0,"initialize":"","finalize":"","libs":[],"x":320,"y":140,"wires":[]},`+
			`{"id":%q,"type":"inject","z":%q,"name":%q,"props":[],"repeat":"","crontab":"","once":false,"onceDelay":0.1,"topic":"","payload":"","payloadType":"str","x":140,"y":140,"wires":[[%q]]}]}`,
		setContextHelperFlowID, setContextHelperFlowLabel,
		functionID, setContextHelperFlowID, setContextHelperFunctionNm, setContextFunctionCode,
		injectID, setContextHelperFlowID, setContextHelperInjectName, functionID))
}

// deniedHelperNodeType returns the node type the configured policy
// forbids among the ones the set_context helper installs, or "" when the
// helper may be provisioned.
//
// The question is asked of the client's policy — the same predicate the
// helper's own AddNode calls are judged by — so the pre-flight answer and
// the enforcement cannot drift apart.
func (s *Server) deniedHelperNodeType() string {
	if s.nrClient == nil {
		return ""
	}
	for _, nodeType := range []string{"function", "inject"} {
		if !s.nrClient.NodeTypeAllowed(nodeType) {
			return nodeType
		}
	}
	return ""
}

// extractFlowID pulls the id out of a CreateFlow response. The
// response shape is the same nested tab document POST /flow accepted
// (or sometimes a flat-array element from the API v1 shape), so the
// only key we care about is "id". An error here means the runtime
// returned something we cannot recognise — better to surface that
// than to proceed with an empty id that AddNode would 404 on.
func extractFlowID(raw nodered.RawFlow) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		var id string
		if err := json.Unmarshal(doc["id"], &id); err == nil && id != "" {
			return id, nil
		}
	}
	// Some Node-RED versions return the flat-array element instead:
	// {"type":"tab","id":"...","label":"..."}.
	var flat struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && flat.ID != "" {
		return flat.ID, nil
	}
	return "", fmt.Errorf("response has no id: %s", string(raw))
}
