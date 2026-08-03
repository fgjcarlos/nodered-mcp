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
// set_context concurrently. provisioningMu guards the lazy provisioning
// step; after it is provisioned, the helper ids are immutable, and
// concurrent POSTs to /inject/:id are serialised by the runtime (which
// already serialises inject triggers per node id).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// setContextHelper holds the runtime-side ids of the helper flow the MCP
// provisions for the set_context tool. Zero value = not provisioned. The
// helper is stable for the lifetime of the Server.
type setContextHelper struct {
	// flowID, injectID, functionID are the Node-RED ids the helper
	// installs in the runtime. They are chosen at provisioning time
	// (deterministic, fixed strings) and never change after.
	flowID     string
	injectID   string
	functionID string

	// provisioningMu guards the lazy "if zero, provision" step. After
	// the first successful provision it is never re-taken — the helper
	// is a Server-lifetime singleton. A single field in Server.ctxHelper
	// would be racy under concurrent first-callers, hence the mutex.
	provisioningMu sync.Mutex
}

// provisioned reports whether the helper has been installed on the
// runtime. It is the only state readers look at: callers running with
// a non-nil ctxHelper can fire injects without taking the mutex.
func (h *setContextHelper) provisioned() bool {
	return h != nil && h.flowID != "" && h.injectID != ""
}

// setContextHelperIDs is the well-known identity of the helper. The
// labels are what shows in the editor; the ids are what GET /flows and
// POST /inject/:id see. They are deliberately stable across runs so
// the same Server can re-discover an already-provisioned helper after
// a restart — though current code does not yet exercise that path
// (helper state is in-memory only).
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

// ensureSetContextHelper lazy-provisions the on-runtime helper used by
// set_context. It is a no-op after the first successful provision.
//
// Why a custom flow tab: the helper is invisible-by-default to the
// user (label begins with __, label prefix is what shows in the
// editor sidebar) and lives in its own tab so the wires, function
// code, and inject trigger stay together even if the user moves
// other nodes around.
//
// Why a mutex and not a sync.Once: the cheap test inside the mutex
// is `if h.provisioned()`, which lets the common case (already
// provisioned) skip the slow part. A sync.Once would also work but
// forces every caller to share the same gate, even on the no-op
// path; the explicit mutex makes that fast path obvious in the
// trace.
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

func (s *Server) ensureSetContextHelper(ctx context.Context) (*setContextHelper, bool, error) {
	if s.readOnly {
		// Defensive: the tool is withheld in read-only mode, so this
		// should never be reached. Surface a clear error rather than
		// silently failing in a less obvious way.
		return nil, false, fmt.Errorf("set_context is not available in read-only mode")
	}

	if s.ctxHelper != nil && s.ctxHelper.provisioned() {
		return s.ctxHelper, false, nil
	}

	s.ctxHelper = &setContextHelper{}
	s.ctxHelper.provisioningMu.Lock()
	defer s.ctxHelper.provisioningMu.Unlock()

	// Re-check under the lock: another goroutine may have just finished
	// provisioning.
	if s.ctxHelper.provisioned() {
		return s.ctxHelper, false, nil
	}

	if err := s.provisionSetContextHelper(ctx); err != nil {
		// Roll back the placeholder so the next call retries from
		// scratch rather than seeing a "provisioned" helper that
		// actually has no flow on the runtime.
		s.ctxHelper = nil
		return nil, false, err
	}
	return s.ctxHelper, true, nil
}

// provisionSetContextHelper performs the one-time install: create the
// flow tab, add the inject (with no wires yet), add the function, then
// wire them. Each step goes through the public Client methods so the
// existing guardrails (wire validation, snapshot-backup, write-mutex)
// apply — same guarantees a user gets when they click the editor.
//
// Wiring order matters: AddNode validates the new node's wires against
// the existing flow, so an inject wired to a not-yet-added function
// fails the check. The function is added first (no upstream wire), then
// the inject (no wires at all), then ConnectNodes establishes the
// inject→function wire on a flow that contains both endpoints.
//
// Runtime id policy: Node-RED's POST /flow IGNORES the id we send
// and assigns one of its own (e.g. d23de851e7ed4098). The
// deterministic ids (setContextHelperFlowID, etc.) are used as the
// *node* ids and as a hint for the tab's `z` field, but the
// surviving flow id is read back from CreateFlow's response. The
// "no further discovery" property still holds: as long as the
// Server is up, s.ctxHelper is the source of truth, and the
// runtime ids never change.
func (s *Server) provisionSetContextHelper(ctx context.Context) error {
	// 1. The flow tab. The id is included in the request body, but
	// Node-RED's POST /flow reassigns it. We capture whatever id
	// the runtime settled on from the response, because the
	// follow-up AddNode / ConnectNodes calls need to address the
	// tab by the runtime's id, not the one we asked for.
	tabDoc := nodered.RawFlow([]byte(fmt.Sprintf(
		`{"id":%q,"label":%q,"nodes":[]}`,
		setContextHelperFlowID, setContextHelperFlowLabel,
	)))
	created, err := s.nrClient.CreateFlow(ctx, tabDoc)
	if err != nil {
		return fmt.Errorf("creating helper flow tab: %w", err)
	}
	flowID, err := extractFlowID(created)
	if err != nil {
		return fmt.Errorf("helper flow id missing from CreateFlow response: %w", err)
	}

	// 2. The function node first. It has no incoming wire so the
	// wire-validation step in AddNode has nothing to check, and
	// installing it before the inject means step 3's inject wires
	// can resolve. The function's id stays at the deterministic
	// constant — Node-RED accepts user-supplied ids for nodes.
	functionNode := json.RawMessage(fmt.Sprintf(
		`{"id":%q,"type":"function","z":%q,"name":%q,"func":%q,"outputs":1,"noerr":0,"initialize":"","finalize":"","libs":[],"x":320,"y":140,"wires":[]}`,
		setContextHelperFunctionID, flowID, setContextHelperFunctionNm, setContextFunctionCode,
	))
	if err := s.nrClient.AddNode(ctx, flowID, functionNode); err != nil {
		return fmt.Errorf("creating helper function: %w", err)
	}

	// 3. The inject node. props:[] is what makes it a bare trigger
	// — no payload, no topic, just a relay that fires whatever the
	// POST /inject/:id body carries (as long as that body has
	// __user_inject_props__). The wires field is empty: the wire
	// to the function is added in the next step via ConnectNodes,
	// which goes through the same AddNode→editFlow write path
	// (and so benefits from the same wire validation).
	injectNode := json.RawMessage(fmt.Sprintf(
		`{"id":%q,"type":"inject","z":%q,"name":%q,"props":[],"repeat":"","crontab":"","once":false,"onceDelay":0.1,"topic":"","payload":"","payloadType":"str","x":140,"y":140,"wires":[]}`,
		setContextHelperInjectID, flowID, setContextHelperInjectName,
	))
	if err := s.nrClient.AddNode(ctx, flowID, injectNode); err != nil {
		return fmt.Errorf("creating helper inject: %w", err)
	}

	// 4. Wire inject → function. With both endpoints in place, the
	// wire-validation step in editFlow accepts the connection.
	if err := s.nrClient.ConnectNodes(ctx, flowID, setContextHelperInjectID, 0, setContextHelperFunctionID); err != nil {
		return fmt.Errorf("wiring helper: %w", err)
	}

	s.ctxHelper.flowID = flowID
	s.ctxHelper.injectID = setContextHelperInjectID
	s.ctxHelper.functionID = setContextHelperFunctionID
	return nil
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
