package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// handleGetDebugMessages returns recent debug-node output from the tail.
func (s *Server) handleGetDebugMessages(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.debugStream {
		return mcp.NewToolResultError(
			"debug streaming is disabled. Set MCP_DEBUG_STREAM=on (or pass --debug-stream) " +
				"to enable the /comms WebSocket tail. On some Node-RED versions the tail " +
				"crashes the runtime; keep it off unless you need it.",
		), nil
	}
	if s.debugTail == nil {
		return mcp.NewToolResultError(
			"debug streaming is not available: the /comms WebSocket endpoint could not " +
				"be derived from the configured Node-RED URL.",
		), nil
	}

	limit := int(req.GetFloat("limit", 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var since time.Time
	if raw := req.GetString("since", ""); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"since must be an RFC 3339 timestamp (e.g. 2026-07-27T08:15:00Z), got %q", raw,
			)), nil
		}
		since = parsed
	}
	slog.Debug("tool: get_debug_messages", "limit", limit, "since", since)

	snap := s.debugTail.Snapshot(limit, since)

	// An empty result has several very different causes, and guessing wrong
	// sends the model off debugging the wrong thing. Name the actual one.
	if len(snap.Messages) == 0 {
		switch {
		case !snap.Connected && snap.LastError != "":
			return mcp.NewToolResultText(fmt.Sprintf(
				"Not connected to Node-RED's debug stream, so nothing has been captured.\n"+
					"Last error: %s", snap.LastError,
			)), nil
		case !snap.Connected:
			return mcp.NewToolResultText(
				"The debug stream is still connecting; nothing captured yet. " +
					"Give it ~3 seconds after server start (or after `initialize` if " +
					"this is a fresh MCP session) and call get_debug_messages again. " +
					"If the message persists, set MCP_LOG_LEVEL=debug and look for " +
					"a /comms dial error in the server logs.",
			), nil
		case snap.Received == 0:
			return mcp.NewToolResultText(
				"Connected to the debug stream, but no debug messages have arrived yet.\n" +
					"Node-RED only emits these from debug nodes that are enabled and wired " +
					"into a path the messages actually take — check the flow has one, then " +
					"trigger it with inject_node.",
			), nil
		default:
			return mcp.NewToolResultText(fmt.Sprintf(
				"No debug messages match that filter (%d buffered, %d received in total).",
				snap.Buffered, snap.Received,
			)), nil
		}
	}

	out, err := json.MarshalIndent(snap.Messages, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding debug messages: %v", err)), nil
	}

	header := fmt.Sprintf("%d debug message(s), oldest first.", len(snap.Messages))
	if snap.Dropped > 0 {
		header += fmt.Sprintf(
			" %d older message(s) were discarded: the buffer holds the most recent %d.",
			snap.Dropped, snap.Buffered,
		)
	}
	if !snap.Connected {
		header += " Warning: the debug stream is currently disconnected, so this may be stale."
	}
	return mcp.NewToolResultText(fmt.Sprintf("%s\n\n```json\n%s\n```", header, string(out))), nil
}

// handleGetRuntimeLogs reads the Node-RED runtime log (issue #51).
//
// The endpoint is GET /logs?count=<limit> on the admin API. As of
// Node-RED 5.x the admin API does not expose this endpoint, so a 404
// is the expected answer; the handler translates it to a clear,
// action-shaped error. When the endpoint IS present (a future
// Node-RED, a fork, or a logging plugin mounted at /logs), the
// response is parsed permissively: the envelope {logs:[...]}, a
// bare array, or plain text all yield a Logs slice.
//
// Filtering happens client-side: the runtime may not support
// server-side `since` or `level`, and the issue explicitly asks for
// them anyway. A model that wants "errors since T" should still get
// it even when the runtime is older than the spec.

// handleGetRuntimeLogs reads the Node-RED runtime log (issue #51).
//
// The endpoint is GET /logs?count=<limit> on the admin API. As of
// Node-RED 5.x the admin API does not expose this endpoint, so a 404
// is the expected answer; the handler translates it to a clear,
// action-shaped error. When the endpoint IS present (a future
// Node-RED, a fork, or a logging plugin mounted at /logs), the
// response is parsed permissively: the envelope {logs:[...]}, a
// bare array, or plain text all yield a Logs slice.
//
// Filtering happens client-side: the runtime may not support
// server-side `since` or `level`, and the issue explicitly asks for
// them anyway. A model that wants "errors since T" should still get
// it even when the runtime is older than the spec.
func (s *Server) handleGetRuntimeLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := int(req.GetFloat("limit", 100))
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	level := strings.ToLower(strings.TrimSpace(req.GetString("level", "")))
	if level != "" && level != "info" && level != "warn" && level != "error" {
		return mcp.NewToolResultError(fmt.Sprintf(
			"level must be one of \"info\", \"warn\", \"error\" (or omitted for all), got %q", level,
		)), nil
	}

	sinceArg := strings.TrimSpace(req.GetString("since", ""))
	var lineOffset int // > 0 means "last N lines"; 0 means no offset
	var sinceTime time.Time
	if sinceArg != "" {
		if strings.HasPrefix(sinceArg, "-") {
			n, err := strconv.Atoi(sinceArg[1:])
			if err != nil || n <= 0 {
				return mcp.NewToolResultError(fmt.Sprintf(
					"since line offset must be a positive integer after \"-\", got %q (e.g. \"-50\")", sinceArg,
				)), nil
			}
			lineOffset = n
		} else {
			t, err := time.Parse(time.RFC3339, sinceArg)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf(
					"since must be an RFC 3339 timestamp (e.g. \"2026-07-28T19:00:00Z\") or a line offset \"-N\", got %q", sinceArg,
				)), nil
			}
			sinceTime = t
		}
	}
	slog.Debug("tool: get_runtime_logs", "limit", limit, "level", level, "since", sinceArg)

	// Request slightly more than the user wants so the post-filter
	// still has a reasonable chance of returning the requested
	// count. A line-offset "since" can only be honoured when the
	// runtime gave us at least N lines; for a timestamp "since" we
	// ask for the full limit and filter on the way out.
	fetch := limit
	if lineOffset > 0 && lineOffset > fetch {
		fetch = lineOffset
	}

	raw, err := s.nrClient.GetRuntimeLogs(ctx, fetch)
	if err != nil {
		slog.Error("get_runtime_logs failed", "error", err)
		if nodered.IsLogsNotFound(err) {
			return mcp.NewToolResultError(
				"Node-RED did not respond on GET /logs. This endpoint is not part of the " +
					"standard Node-RED 5.x admin API; it may be exposed by a future version, a " +
					"fork, or a logging plugin mounted at /logs. To read the runtime log on a " +
					"stock install, point at Node-RED's stdout (the file is named " +
					"~/.node-red/*.log or the container's stdout).",
			), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}

	filtered := filterLogs(raw, level, sinceTime, lineOffset, limit)

	if len(filtered) == 0 {
		return mcp.NewToolResultText(
			"No runtime log entries match that filter. The runtime may have rotated " +
				"the buffer, or the filter excluded everything (level too narrow, " +
				"timestamp in the future). Loosen the filter and try again.",
		), nil
	}

	var sb strings.Builder
	for _, e := range filtered {
		writeLogLine(&sb, e)
	}
	return mcp.NewToolResultText(sb.String()), nil
}

// writeLogLine renders one LogEntry as a single line of text. The
// timestamp is omitted when zero (plain-text bodies never carry
// one) and the level is uppercased for readability.

// writeLogLine renders one LogEntry as a single line of text. The
// timestamp is omitted when zero (plain-text bodies never carry
// one) and the level is uppercased for readability.
func writeLogLine(sb *strings.Builder, e nodered.LogEntry) {
	if !e.Timestamp.IsZero() {
		sb.WriteString(e.Timestamp.UTC().Format(time.RFC3339))
		sb.WriteByte(' ')
	}
	if e.Level != "" {
		sb.WriteString(strings.ToUpper(e.Level))
		sb.WriteByte(' ')
	}
	sb.WriteString(e.Message)
	sb.WriteByte('\n')
}

// filterLogs applies level / since / line-offset filtering to a Logs
// slice. The rules are intentionally simple: the runtime may have
// returned a million entries and the user asked for a thousand
// filtered ones, so we walk in order rather than sorting.
//
//   - level: keep entries whose normalised level matches.
//   - sinceTime: keep entries with a timestamp >= sinceTime; entries
//     without a timestamp are dropped (we cannot tell which side of
//     a cutoff they fall on).
//   - lineOffset: keep the last N entries (after the other filters
//     have narrowed the slice).
//   - limit: cap the result to limit entries.

// filterLogs applies level / since / line-offset filtering to a Logs
// slice. The rules are intentionally simple: the runtime may have
// returned a million entries and the user asked for a thousand
// filtered ones, so we walk in order rather than sorting.
//
//   - level: keep entries whose normalised level matches.
//   - sinceTime: keep entries with a timestamp >= sinceTime; entries
//     without a timestamp are dropped (we cannot tell which side of
//     a cutoff they fall on).
//   - lineOffset: keep the last N entries (after the other filters
//     have narrowed the slice).
//   - limit: cap the result to limit entries.
func filterLogs(in nodered.Logs, level string, sinceTime time.Time, lineOffset, limit int) nodered.Logs {
	out := in
	if level != "" {
		out = filterLogsByLevel(out, level)
	}
	if !sinceTime.IsZero() {
		out = filterLogsByTime(out, sinceTime)
	}
	if lineOffset > 0 && len(out) > lineOffset {
		out = out[len(out)-lineOffset:]
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func filterLogsByLevel(in nodered.Logs, level string) nodered.Logs {
	out := make(nodered.Logs, 0, len(in))
	for _, e := range in {
		if e.Level == level {
			out = append(out, e)
		}
	}
	return out
}

func filterLogsByTime(in nodered.Logs, since time.Time) nodered.Logs {
	out := make(nodered.Logs, 0, len(in))
	for _, e := range in {
		if e.Timestamp.IsZero() {
			continue
		}
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// handleGetNodeStatus returns the last-known runtime status of a
// node (issue #51). The data is fed by the /comms WebSocket via a
// StatusTail that subscribes to status/# events. When the WebSocket
// is not connected (MCP_DEBUG_STREAM off, or the URL could not be
// derived from the configured base), the tool returns a clear
// "stream unavailable" error rather than empty data.
//
// Two query shapes:
//
//   - node_id: the cached status of one node.
//   - flow_id: every node in the flow, joined with the cache. Nodes
//     in the flow but never seen by the tail are reported as
//     "unknown" (per the issue's spec: "never seen an event for
//     that node_id").
//
// The query is read-only: no side effect is triggered on the
// runtime, the admin API is touched only when flow_id is given
// (to resolve the flow's node ids) and only on a GET.

// handleGetNodeStatus returns the last-known runtime status of a
// node (issue #51). The data is fed by the /comms WebSocket via a
// StatusTail that subscribes to status/# events. When the WebSocket
// is not connected (MCP_DEBUG_STREAM off, or the URL could not be
// derived from the configured base), the tool returns a clear
// "stream unavailable" error rather than empty data.
//
// Two query shapes:
//
//   - node_id: the cached status of one node.
//   - flow_id: every node in the flow, joined with the cache. Nodes
//     in the flow but never seen by the tail are reported as
//     "unknown" (per the issue's spec: "never seen an event for
//     that node_id").
//
// The query is read-only: no side effect is triggered on the
// runtime, the admin API is touched only when flow_id is given
// (to resolve the flow's node ids) and only on a GET.
func (s *Server) handleGetNodeStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	nodeID := strings.TrimSpace(req.GetString("node_id", ""))
	flowID := strings.TrimSpace(req.GetString("flow_id", ""))

	if nodeID != "" && flowID != "" {
		return mcp.NewToolResultError(
			"node_id and flow_id are mutually exclusive: pass one or the other, not both.",
		), nil
	}
	if nodeID == "" && flowID == "" {
		return mcp.NewToolResultError(
			"either node_id or flow_id is required.",
		), nil
	}

	if !s.debugStream {
		return mcp.NewToolResultError(
			"node status is not available: the /comms WebSocket is not connected. " +
				"Set MCP_DEBUG_STREAM=on to enable it. On some Node-RED versions the " +
				"tail crashes the runtime; keep it off unless you need it.",
		), nil
	}
	if s.statusTail == nil {
		return mcp.NewToolResultError(
			"node status is not available: the /comms WebSocket endpoint could not be " +
				"derived from the configured Node-RED URL.",
		), nil
	}

	slog.Debug("tool: get_node_status", "node_id", nodeID, "flow_id", flowID)

	// Single-node path: read the cache, render the result.
	if nodeID != "" {
		entry, ok := s.statusTail.Lookup(nodeID)
		return s.renderNodeStatus(nodeID, entry, ok)
	}

	// Flow path: resolve the flow's node ids, then ask the tail
	// for a filtered snapshot. A 404 on the flow is a real error
	// (the flow id is wrong); a 5xx is a runtime problem; both
	// are surfaced verbatim so the operator can act on them.
	raw, err := s.nrClient.GetFlow(ctx, flowID)
	if err != nil {
		slog.Error("get_node_status: GetFlow failed", "error", err, "flow_id", flowID)
		return mcp.NewToolResultError(fmt.Sprintf("reading flow %q: %v", flowID, err)), nil
	}
	ids := nodered.ParseFlowNodeIDs(raw)
	if len(ids) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf(
			"flow %q has no nodes (or its shape is not what nodered-mcp expected)", flowID,
		)), nil
	}
	snap := s.statusTail.Snapshot(ids...)
	return s.renderFlowStatus(flowID, ids, snap)
}

// renderNodeStatus turns one (entry, ok) pair into the MCP reply.
// The audit's acceptance criteria live here: known connected →
// connected=true; known errored → connected=false + last_error.

// renderNodeStatus turns one (entry, ok) pair into the MCP reply.
// The audit's acceptance criteria live here: known connected →
// connected=true; known errored → connected=false + last_error.
func (s *Server) renderNodeStatus(nodeID string, entry nodered.StatusEntry, ok bool) (*mcp.CallToolResult, error) {
	if !ok {
		// Never-seen: the runtime has not published any status
		// for this id. Render it as "unknown" rather than
		// "disconnected" — disconnected implies a transition
		// the cache has not recorded.
		return mcp.NewToolResultText(fmt.Sprintf(
			"Node %q: unknown (no status events seen on the /comms stream).\n"+
				"The node may be a config node, a freshly-deployed node, or an id the "+
				"model guessed. Wait a few seconds (a deploy is needed for new nodes to "+
				"start emitting status) and try again.",
			nodeID,
		)), nil
	}
	return mcp.NewToolResultText(formatStatusEntry(nodeID, entry)), nil
}

// renderFlowStatus turns a flow-filtered snapshot into the MCP
// reply. Every node in the flow gets a line; nodes the tail never
// saw are rendered as "unknown".

// renderFlowStatus turns a flow-filtered snapshot into the MCP
// reply. Every node in the flow gets a line; nodes the tail never
// saw are rendered as "unknown".
func (s *Server) renderFlowStatus(flowID string, nodeIDs []string, snap nodered.StatusSnapshot) (*mcp.CallToolResult, error) {
	byID := make(map[string]nodered.StatusEntry, len(snap.Entries))
	for _, e := range snap.Entries {
		byID[e.ID] = e
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Status of %d node(s) in flow %q:", len(nodeIDs), flowID)
	if !snap.Connected {
		fmt.Fprintf(&sb, "\nWarning: the status stream is currently disconnected%s",
			statusTailLastError(snap.LastError))
	}
	fmt.Fprintf(&sb, "\nTracked: %d node id(s) known to the tail.", snap.Tracked)
	for _, id := range nodeIDs {
		e, ok := byID[id]
		if !ok {
			fmt.Fprintf(&sb, "\n- %s: unknown (no status events seen)", id)
			continue
		}
		fmt.Fprintf(&sb, "\n- %s", formatStatusEntry(id, e))
	}
	return mcp.NewToolResultText(sb.String()), nil
}

// statusTailLastError renders the parenthetical " (last error: ...)"
// we append to the disconnected warning. Empty when there is no
// recorded error, so the message stays tight.

// statusTailLastError renders the parenthetical " (last error: ...)"
// we append to the disconnected warning. Empty when there is no
// recorded error, so the message stays tight.
func statusTailLastError(s string) string {
	if s == "" {
		return ""
	}
	return " (last error: " + s + ")"
}

// formatStatusEntry renders one entry as a single human-readable
// line. The shape is what the audit's acceptance criteria
// describe: connected=true|false plus, on a failure, the last
// error text.

// formatStatusEntry renders one entry as a single human-readable
// line. The shape is what the audit's acceptance criteria
// describe: connected=true|false plus, on a failure, the last
// error text.
func formatStatusEntry(nodeID string, e nodered.StatusEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Node %q: status=%s", nodeID, e.Status)
	if e.Text != "" {
		fmt.Fprintf(&b, ", text=%q", e.Text)
	}
	if e.Fill != "" || e.Shape != "" {
		fmt.Fprintf(&b, " (fill=%s, shape=%s)", e.Fill, e.Shape)
	}
	if e.LastError != "" {
		fmt.Fprintf(&b, ", last_error=%q", e.LastError)
	}
	if !e.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, ", updated_at=%s", e.UpdatedAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}

// handleGetContext reads a context store, or one key within it.

// handleGetFlowsState returns the current runtime state of Node-RED.
func (s *Server) handleGetFlowsState(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_flows_state")

	raw, err := s.nrClient.GetFlowsState(ctx)
	if err != nil {
		slog.Error("get_flows_state failed", "error", err)
		// Same runtimeState gate as set_flows_state -- the GET endpoint is
		// only mounted alongside the POST one.
		var apiErr *nodered.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return mcp.NewToolResultError(
				"Node-RED did not respond on GET /flows/state. This endpoint is " +
					"only mounted when settings.runtimeState.enabled is true. Add " +
					"`runtimeState: { enabled: true, ui: false }` to your " +
					"settings.js and restart Node-RED, then retry.",
			), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleSetFlowsState starts or stops the Node-RED runtime.

// handleSetFlowsState starts or stops the Node-RED runtime.
func (s *Server) handleSetFlowsState(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	state, err := req.RequireString("state")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: set_flows_state", "state", state)

	if err := s.nrClient.SetFlowsState(ctx, state); err != nil {
		slog.Error("set_flows_state failed", "error", err, "state", state)
		// Node-RED only mounts POST /flows/state when settings.runtimeState.enabled
		// is true. Out of the box it is false, so a fresh deploy sees a bare
		// 404 with no actionable hint -- surface the fix here rather than in
		// the runtime-layer generic hint, which would not name the setting.
		var apiErr *nodered.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return mcp.NewToolResultError(
				"Node-RED did not respond on POST /flows/state. This endpoint is " +
					"only mounted when settings.runtimeState.enabled is true. Add " +
					"`runtimeState: { enabled: true, ui: false }` to your " +
					"settings.js and restart Node-RED, then retry.",
			), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Node-RED runtime %sed (a backup was taken first).", state)), nil
}

// handleSetFlows replaces the entire flow config with a full deployment.
// The flows argument accepts either a JSON-encoded string OR a flow array
// directly.

// handleSetFlows replaces the entire flow config with a full deployment.
// The flows argument accepts either a JSON-encoded string OR a flow array
// directly.
func (s *Server) handleSetFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := flowsParam(req, "flows")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	flows, err := normalizeFlowsArray(raw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if denied, t := s.findDeniedNodeInFlowsArray(flows); denied {
		return mcp.NewToolResultError(fmt.Sprintf(
			"node type %q is in MCP_NODE_DENYLIST; remove it from the denylist or use a different node type (see SECURITY.md)",
			t,
		)), nil
	}
	slog.Debug("tool: set_flows")

	if err := s.nrClient.SetFlows(ctx, flows); err != nil {
		slog.Error("set_flows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText("Flows deployed (a backup was taken first)."), nil
}

// handleSearchNodes queries the public npm registry for node-red-* modules.
