package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// handleListFlows returns a map of the instance, compact by default.
//
// The full flow config of a real instance runs to tens of thousands of tokens,
// so dumping it on every call would spend the context before any work starts.
// Summary is therefore the default and "full" is opt-in.
func (s *Server) handleListFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	detail := req.GetString("detail", "summary")
	if detail != "summary" && detail != "full" {
		return mcp.NewToolResultError(fmt.Sprintf("detail must be \"summary\" or \"full\", got %q", detail)), nil
	}
	force := req.GetBool("force", false)
	slog.Debug("tool: list_flows", "detail", detail, "force", force)

	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		slog.Error("list_flows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	if len(raw) == 0 {
		raw = []byte("[]")
	}

	if detail == "full" {
		nodeCount := nodered.SummarizeFlows(raw).TotalNodes
		if !force && nodeCount > s.listFlowsFullThreshold {
			return mcp.NewToolResultText(fmt.Sprintf(
				"This flow config has %d nodes which may exhaust the model context. "+
					"Pass force=true to retrieve it anyway, or use get_flow to fetch one tab at a time.",
				nodeCount,
			)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Found %d flow tab(s) in Node-RED (full configuration).\n\n```json\n%s\n```",
			nodered.FlowTabCount(raw), prettyJSON(raw),
		)), nil
	}

	overview := nodered.SummarizeFlows(raw)
	out, err := json.MarshalIndent(overview, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding summary: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"%d flow tab(s), %d subflow(s), %d node(s) total.\n\n```json\n%s\n```\n\n"+
			"This is a summary: node bodies are omitted. Use search_flows to locate "+
			"specific nodes, get_flow to fetch one tab in full, or list_flows with "+
			"detail=\"full\" for the entire configuration.",
		len(overview.Tabs), len(overview.Subflows), overview.TotalNodes, string(out),
	)), nil
}

// handleSearchFlows finds nodes across every flow without returning them all.

// handleSearchFlows finds nodes across every flow without returning them all.
func (s *Server) handleSearchFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := req.GetString("query", "")
	nodeType := req.GetString("type", "")
	// mcp-go hands number arguments over as float64.
	limit := int(req.GetFloat("limit", 20))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	slog.Debug("tool: search_flows", "query", query, "type", nodeType, "limit", limit)

	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		slog.Error("search_flows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}

	matches, total := nodered.SearchFlows(raw, query, nodeType, limit)
	if total == 0 {
		return mcp.NewToolResultText(fmt.Sprintf(
			"No nodes matched (query=%q, type=%q). Try a broader query, or list_flows "+
				"to see which node types exist.", query, nodeType,
		)), nil
	}
	out, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding matches: %v", err)), nil
	}

	// Say so when the list was cut short: a bare list of 20 reads as "there are
	// exactly 20" and the model would stop looking.
	header := fmt.Sprintf("%d node(s) matched.", total)
	if total > len(matches) {
		header = fmt.Sprintf(
			"%d node(s) matched; showing the first %d. Raise limit or narrow the query to see the rest.",
			total, len(matches),
		)
	}
	return mcp.NewToolResultText(fmt.Sprintf("%s\n\n```json\n%s\n```", header, string(out))), nil
}

// handleGetFlow returns a single flow tab as JSON.

// handleGetFlow returns a single flow tab as JSON.
func (s *Server) handleGetFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: get_flow", "id", id)

	raw, err := s.nrClient.GetFlow(ctx, id)
	if err != nil {
		slog.Error("get_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleCreateFlow creates a new flow tab from a JSON document. The flow
// argument accepts either a JSON-encoded string OR a flow object directly;
// either shape is the right thing for an LLM that does not want to serialize
// a literal by hand.

// handleCreateFlow creates a new flow tab from a JSON document. The flow
// argument accepts either a JSON-encoded string OR a flow object directly;
// either shape is the right thing for an LLM that does not want to serialize
// a literal by hand.
func (s *Server) handleCreateFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := flowParam(req, "flow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	flow, err := normalizeFlowDoc(raw, "", true)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if denied, t := s.findDeniedNodeInFlow(flow); denied {
		return mcp.NewToolResultError(fmt.Sprintf(
			"node type %q is in MCP_NODE_DENYLIST; remove it from the denylist or use a different node type (see SECURITY.md)",
			t,
		)), nil
	}
	slog.Debug("tool: create_flow")

	created, err := s.nrClient.CreateFlow(ctx, flow)
	if err != nil {
		slog.Error("create_flow failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow created.\n\n```json\n%s\n```", prettyJSON(created))), nil
}

// handleUpdateFlow replaces an existing flow tab with a new JSON document.
// The flow argument accepts either a JSON-encoded string or a flow object.

// handleUpdateFlow replaces an existing flow tab with a new JSON document.
// The flow argument accepts either a JSON-encoded string or a flow object.
func (s *Server) handleUpdateFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	raw, err := flowParam(req, "flow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	flow, err := normalizeFlowDoc(raw, id, false)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if denied, t := s.findDeniedNodeInFlow(flow); denied {
		return mcp.NewToolResultError(fmt.Sprintf(
			"node type %q is in MCP_NODE_DENYLIST; remove it from the denylist or use a different node type (see SECURITY.md)",
			t,
		)), nil
	}
	slog.Debug("tool: update_flow", "id", id)

	if err := s.nrClient.UpdateFlow(ctx, id, flow); err != nil {
		slog.Error("update_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow %q updated (a backup was taken first).", id)), nil
}

// handleAddNode appends one node to a flow tab. The node argument accepts
// either a JSON-encoded string or a node object directly.

// handleAddNode appends one node to a flow tab. The node argument accepts
// either a JSON-encoded string or a node object directly.
func (s *Server) handleAddNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowID, err := req.RequireString("flow_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	node, err := nodeParam(req, "node")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if denied, t := s.findDeniedNodeInNode(node); denied {
		return mcp.NewToolResultError(fmt.Sprintf(
			"node type %q is in MCP_NODE_DENYLIST; remove it from the denylist or use a different node type (see SECURITY.md)",
			t,
		)), nil
	}
	slog.Debug("tool: add_node", "flow_id", flowID)

	if err := s.nrClient.AddNode(ctx, flowID, node); err != nil {
		slog.Error("add_node failed", "error", err, "flow_id", flowID)
		return mcp.NewToolResultError(fmt.Sprintf("adding node: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Node added to flow %q (a backup was taken first). Wire it up with connect_nodes.", flowID,
	)), nil
}

// handleUpdateNode merges properties into one node.

// handleUpdateNode merges properties into one node.
func (s *Server) handleUpdateNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowID, err := req.RequireString("flow_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	nodeID, err := req.RequireString("node_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	props, err := req.RequireString("properties")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: update_node", "flow_id", flowID, "node_id", nodeID)

	// Decoding into raw messages keeps each value exactly as supplied — an
	// integer stays an integer, a nested object keeps its own shape.
	var patch map[string]json.RawMessage
	if err := json.Unmarshal([]byte(props), &patch); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"properties must be a JSON object, e.g. {\"topic\":\"home/new\"}: %v", err,
		)), nil
	}

	if err := s.nrClient.UpdateNode(ctx, flowID, nodeID, patch); err != nil {
		slog.Error("update_node failed", "error", err, "flow_id", flowID, "node_id", nodeID)
		return mcp.NewToolResultError(fmt.Sprintf("updating node: %v", err)), nil
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return mcp.NewToolResultText(fmt.Sprintf(
		"Node %q updated: %s. Every other property was left untouched (a backup was taken first).",
		nodeID, strings.Join(keys, ", "),
	)), nil
}

// handleDeleteNode removes one node and the wires pointing at it.

// handleDeleteNode removes one node and the wires pointing at it.
func (s *Server) handleDeleteNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowID, err := req.RequireString("flow_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	nodeID, err := req.RequireString("node_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: delete_node", "flow_id", flowID, "node_id", nodeID)

	if err := s.nrClient.DeleteNode(ctx, flowID, nodeID); err != nil {
		slog.Error("delete_node failed", "error", err, "flow_id", flowID, "node_id", nodeID)
		return mcp.NewToolResultError(fmt.Sprintf("deleting node: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Node %q deleted from flow %q, along with any wires pointing at it (a backup was taken first).",
		nodeID, flowID,
	)), nil
}

// handleConnectNodes wires one node's output port to another node.

// handleConnectNodes wires one node's output port to another node.
func (s *Server) handleConnectNodes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowID, err := req.RequireString("flow_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromID, err := req.RequireString("from_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	toID, err := req.RequireString("to_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	port := int(req.GetFloat("port", 0))
	if port < 0 || port > 999 {
		return mcp.NewToolResultError(fmt.Sprintf("port %d is out of range (must be 0-999)", port)), nil
	}
	if fromID == toID {
		return mcp.NewToolResultError("from_id and to_id must differ (wiring a node to itself creates an infinite message loop)"), nil
	}
	slog.Debug("tool: connect_nodes", "flow_id", flowID, "from", fromID, "port", port, "to", toID)

	if err := s.nrClient.ConnectNodes(ctx, flowID, fromID, port, toID); err != nil {
		slog.Error("connect_nodes failed", "error", err, "flow_id", flowID)
		return mcp.NewToolResultError(fmt.Sprintf("connecting nodes: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Wired %q output %d to %q (a backup was taken first).", fromID, port, toID,
	)), nil
}

// handleDeleteFlow deletes a flow tab and all its nodes.

// handleDeleteFlow deletes a flow tab and all its nodes.
func (s *Server) handleDeleteFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: delete_flow", "id", id)

	if err := s.nrClient.DeleteFlow(ctx, id); err != nil {
		slog.Error("delete_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow %q deleted (a backup was taken first).", id)), nil
}

// handleValidateFlow runs the structural checks against an in-memory flow
// document. It does not contact the runtime. The "flow" argument accepts
// either a JSON-encoded string, a flow object directly, or a flat array
// of tabs and nodes — the same shapes create_flow / update_flow accept
// — so a model can copy the same payload it would have written, run
// validate_flow over it, and only call the write tool after the issue
// list comes back empty.

// handleValidateFlow runs the structural checks against an in-memory flow
// document. It does not contact the runtime. The "flow" argument accepts
// either a JSON-encoded string, a flow object directly, or a flat array
// of tabs and nodes — the same shapes create_flow / update_flow accept
// — so a model can copy the same payload it would have written, run
// validate_flow over it, and only call the write tool after the issue
// list comes back empty.
func (s *Server) handleValidateFlow(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := flowParam(req, "flow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: validate_flow")

	issues := nodered.ValidateFlows(nodered.RawFlow(raw))
	resp := struct {
		OK     bool                `json:"ok"`
		Issues []nodered.FlowIssue `json:"issues"`
	}{
		OK:     len(issues) == 0,
		Issues: issues,
	}
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding response: %v", err)), nil
	}
	if resp.OK {
		return mcp.NewToolResultText(fmt.Sprintf("ok — 0 issues\n\n```json\n%s\n```", string(out))), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"%d issue(s) found:\n\n```json\n%s\n```", len(issues), string(out),
	)), nil
}

// handleDisableFlow stops a flow tab from running without deleting it.

// handleDisableFlow stops a flow tab from running without deleting it.
func (s *Server) handleDisableFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: disable_flow", "id", id)

	if err := s.nrClient.SetFlowDisabled(ctx, id, true); err != nil {
		slog.Error("disable_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow %q disabled (a backup was taken first).", id)), nil
}

// handleEnableFlow re-enables a previously disabled flow tab.

// handleEnableFlow re-enables a previously disabled flow tab.
func (s *Server) handleEnableFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: enable_flow", "id", id)

	if err := s.nrClient.SetFlowDisabled(ctx, id, false); err != nil {
		slog.Error("enable_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow %q enabled (a backup was taken first).", id)), nil
}

// handleInjectNode triggers an inject node, optionally with a custom
// payload.
//
// The no-payload path is the original behaviour: InjectNode looks up
// the node's type first (issue #43/#56) and refuses to fire anything
// that is not type:"inject", so the operator sees a typed error
// instead of a phantom success.
//
// The with-payload path goes through InjectNodeWithBody (added by
// issue #52 for set_context) with the magic __user_inject_props__
// trigger that makes Node-RED 5.x forward the body to node.receive.
// The trigger's value is the inject's per-call prop override list;
// an empty array means "use this body as msg", which is what we
// want when the operator is commissioning a flow with a specific
// input. Anything else the caller put in the payload (topic,
// headers, custom keys) round-trips through as part of msg.

// handleInjectNode triggers an inject node, optionally with a custom
// payload.
//
// The no-payload path is the original behaviour: InjectNode looks up
// the node's type first (issue #43/#56) and refuses to fire anything
// that is not type:"inject", so the operator sees a typed error
// instead of a phantom success.
//
// The with-payload path goes through InjectNodeWithBody (added by
// issue #52 for set_context) with the magic __user_inject_props__
// trigger that makes Node-RED 5.x forward the body to node.receive.
// The trigger's value is the inject's per-call prop override list;
// an empty array means "use this body as msg", which is what we
// want when the operator is commissioning a flow with a specific
// input. Anything else the caller put in the payload (topic,
// headers, custom keys) round-trips through as part of msg.
func (s *Server) handleInjectNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if id == "" {
		// Reject before any HTTP call — the underlying InjectNode
		// refuses empty ids too, but doing it here keeps the
		// validation error surfaced before the disabled/lookup
		// checks (which would otherwise race to "node not found").
		return mcp.NewToolResultError("id is required"), nil
	}
	slog.Debug("tool: inject_node", "id", id)

	// Look at the args map directly so we can tell "key not present"
	// (no payload supplied — original behaviour) from "key present
	// but empty" (caller passed a payload that happens to encode to
	// an empty object). GetArguments returns the whole map; an
	// absent key means nil. Payload validation is pure (no HTTP)
	// and runs first so a malformed payload never reaches the
	// runtime.
	args := req.GetArguments()
	rawPayload, hasPayload := args["payload"]
	var payload json.RawMessage
	if hasPayload && rawPayload != nil {
		encoded, encErr := encodePayloadArg(rawPayload)
		if encErr != nil {
			return mcp.NewToolResultError(encErr.Error()), nil
		}
		payload = encoded
	}

	// Reject disabled candidates before touching the wire. The
	// admin /inject/:id endpoint accepts the call regardless and
	// returns success — the runtime then silently drops the
	// message when the node or its tab is disabled. Issue #104
	// caught a model disabling a tab and then injecting a node
	// inside it: the fire "succeeded" while nothing happened
	// downstream. Look up the target so the operator sees a typed
	// error naming the actual cause.
	lookup, found, lookupErr := s.nrClient.LookupInjectTarget(ctx, id)
	if lookupErr != nil {
		slog.Error("inject_node lookup failed", "error", lookupErr, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("looking up node %q: %v", id, lookupErr)), nil
	}
	if !found {
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found in any tab", id)), nil
	}
	if lookup.Disabled {
		return mcp.NewToolResultError(fmt.Sprintf("node %q is disabled", id)), nil
	}
	if lookup.TabDisabled {
		return mcp.NewToolResultError(fmt.Sprintf("node %q is in a disabled tab", id)), nil
	}

	if !hasPayload || rawPayload == nil {
		if err := s.nrClient.InjectNode(ctx, id); err != nil {
			slog.Error("inject_node failed", "error", err, "id", id)
			return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Inject node %q fired.", id)), nil
	}

	// Build the wire body. The trigger must come last so a caller
	// who literally put "__user_inject_props__" in their payload
	// cannot shadow it (we overwrite, not merge).
	body := buildInjectPayloadBody(payload)
	if err := s.nrClient.InjectNodeWithBody(ctx, id, body); err != nil {
		slog.Error("inject_node failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Inject node %q fired with payload.", id)), nil
}

// encodePayloadArg normalises a payload argument that arrived as
// either a JSON object (map[string]any) or a JSON-encoded string
// into a single json.RawMessage. Anything else is a caller mistake
// and gets a typed error before the runtime is touched.

// encodePayloadArg normalises a payload argument that arrived as
// either a JSON object (map[string]any) or a JSON-encoded string
// into a single json.RawMessage. Anything else is a caller mistake
// and gets a typed error before the runtime is touched.
func encodePayloadArg(v any) (json.RawMessage, error) {
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, errors.New("payload must be a JSON object or a JSON-encoded string")
		}
		return raw, nil
	case map[string]any:
		out, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding payload: %w", err)
		}
		return out, nil
	case []any:
		out, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding payload: %w", err)
		}
		return out, nil
	default:
		return nil, errors.New("payload must be a JSON object or a JSON-encoded string")
	}
}

// buildInjectPayloadBody wraps a payload into the body shape
// Node-RED 5.x's /inject/:id handler expects. The trigger field
// name and value are documented on Client.InjectNodeWithBody.

// buildInjectPayloadBody wraps a payload into the body shape
// Node-RED 5.x's /inject/:id handler expects. The trigger field
// name and value are documented on Client.InjectNodeWithBody.
func buildInjectPayloadBody(payload json.RawMessage) json.RawMessage {
	// {"<payload fields>", "__user_inject_props__":[]}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		// payload is a JSON scalar or array: wrap it as a fresh object
		// under "payload" so the caller's value still reaches the
		// inject's msg.
		wrapped, _ := json.Marshal(map[string]json.RawMessage{
			"payload":               payload,
			"__user_inject_props__": json.RawMessage(`[]`),
		})
		return wrapped
	}
	m["__user_inject_props__"] = json.RawMessage(`[]`)
	out, err := json.Marshal(m)
	if err != nil {
		// Marshal of a map that already parsed cleanly cannot
		// fail; fall back to the wrapped form so we still send
		// something usable.
		wrapped, _ := json.Marshal(map[string]json.RawMessage{
			"payload":               payload,
			"__user_inject_props__": json.RawMessage(`[]`),
		})
		return wrapped
	}
	return out
}

// handleListSubflows returns every subflow definition installed in
// the runtime, as a JSON array.
