package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// registerTools wires every MCP tool to its handler.
//
// Each tool is registered through addReadTool or addWriteTool. That choice is
// the single classification point for read-only mode: a tool registered as a
// write tool is withheld when the server runs read-only, so the decision is
// made here, once, next to the tool's description — not at call time.
func (s *Server) registerTools() {
	// ---- list_flows ----------------------------------------------------
	listFlows := mcp.NewTool("list_flows",
		mcp.WithDescription(
			"Map the connected Node-RED instance: its flow tabs, subflows, how many "+
				"nodes each one owns, and which node types they contain. Use this as "+
				"the entry point when you don't know the layout of the runtime.\n\n"+
				"Returns a compact summary by default, without node bodies. To act on "+
				"specific nodes, follow up with search_flows (find nodes anywhere) or "+
				"get_flow (fetch one tab in full). Pass detail=\"full\" only when you "+
				"genuinely need every node of every tab at once — on a real instance "+
				"that response is very large.",
		),
		mcp.WithString("detail",
			mcp.Description("\"summary\" (default) for the compact map, or \"full\" for the entire raw flow config."),
			mcp.Enum("summary", "full"),
		),
	)
	s.addReadTool(listFlows, s.handleListFlows)

	// ---- search_flows --------------------------------------------------
	searchFlows := mcp.NewTool("search_flows",
		mcp.WithDescription(
			"Find nodes across every flow without downloading the whole configuration. "+
				"Filter by free text, by node type, or both.\n\n"+
				"The text query is matched case-insensitively against each node's full "+
				"JSON, so it finds values that live in node-specific fields: an MQTT "+
				"topic, an HTTP url, a node name, even a line inside a function body. "+
				"Each hit reports the node verbatim plus the tab it belongs to, which "+
				"is what you need to then call get_flow or update_flow. Read-only.",
		),
		mcp.WithString("query",
			mcp.Description("Free text to look for, e.g. \"home/temp\", \"api.example.com\", \"parseTemperature\".")),
		mcp.WithString("type",
			mcp.Description("Exact node type to filter by, e.g. \"mqtt in\", \"function\", \"http request\", \"mqtt-broker\".")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum nodes to return (1-100, default 20). The response always reports the true total.")),
	)
	s.addReadTool(searchFlows, s.handleSearchFlows)

	// ---- get_flow ------------------------------------------------------
	getFlow := mcp.NewTool("get_flow",
		mcp.WithDescription(
			"Fetch a single Node-RED flow tab by its ID, returned as its full "+
				"JSON document (tab metadata plus every node it owns).",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID (as shown by list_flows).")),
	)
	s.addReadTool(getFlow, s.handleGetFlow)

	// ---- create_flow ---------------------------------------------------
	createFlow := mcp.NewTool("create_flow",
		mcp.WithDescription(
			"Create a new Node-RED flow tab. Pass the full flow JSON document "+
				"(at minimum {\"label\":\"...\",\"nodes\":[...]}). Node-RED assigns "+
				"the ID and returns the created flow. A backup is taken first.",
		),
		mcp.WithString("flow", mcp.Required(),
			mcp.Description("The flow document as a JSON string.")),
	)
	s.addWriteTool(createFlow, s.handleCreateFlow)

	// ---- update_flow ---------------------------------------------------
	updateFlow := mcp.NewTool("update_flow",
		mcp.WithDescription(
			"Replace an existing flow tab with a new JSON document (PUT semantics: "+
				"the whole flow is replaced). Read the flow first with get_flow, modify "+
				"it, and send it back intact — every node field is preserved. A backup "+
				"of the current config is taken before the write.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to replace.")),
		mcp.WithString("flow", mcp.Required(),
			mcp.Description("The complete new flow document as a JSON string.")),
	)
	s.addWriteTool(updateFlow, s.handleUpdateFlow)

	// ---- add_node -------------------------------------------------------
	addNode := mcp.NewTool("add_node",
		mcp.WithDescription(
			"Add one node to an existing flow tab, without touching any other node.\n\n"+
				"Prefer this over update_flow: rewriting a whole tab means reproducing "+
				"every node exactly, and any field not reproduced is destroyed. This "+
				"appends and leaves the rest of the tab byte-for-byte identical. A backup "+
				"is taken and the wires are validated before the write.\n\n"+
				"The node needs at least an id and a type. Wire it up afterwards with "+
				"connect_nodes rather than hand-writing the wires array.",
		),
		mcp.WithString("flow_id", mcp.Required(),
			mcp.Description("The flow tab to add the node to (from list_flows or search_flows).")),
		mcp.WithObject("node", mcp.Required(),
			mcp.Description(`The node as a JSON object, e.g. {"id":"n7","type":"debug","z":"tabA","wires":[]}. A JSON-encoded string is also accepted.`),
			mcp.AdditionalProperties(true),
		),
	)
	s.addWriteTool(addNode, s.handleAddNode)

	// ---- update_node ----------------------------------------------------
	updateNode := mcp.NewTool("update_node",
		mcp.WithDescription(
			"Change specific properties of one node, leaving everything else alone.\n\n"+
				"Only the keys you supply are replaced. Every other property survives "+
				"untouched, including ones specific to that node type. This is the safe "+
				"way to retune a node: change an MQTT topic, an HTTP url, a function "+
				"body, or a name without risking the rest of the flow.\n\n"+
				"A node's id cannot be changed, because the wires reference it. A backup "+
				"is taken and the wires are validated before the write.",
		),
		mcp.WithString("flow_id", mcp.Required(),
			mcp.Description("The flow tab that owns the node.")),
		mcp.WithString("node_id", mcp.Required(),
			mcp.Description("The node to change (from search_flows or get_flow).")),
		mcp.WithString("properties", mcp.Required(),
			mcp.Description(`A JSON object of properties to merge, e.g. {"topic":"home/new","qos":"2"}.`)),
	)
	s.addWriteTool(updateNode, s.handleUpdateNode)

	// ---- delete_node ----------------------------------------------------
	deleteNode := mcp.NewTool("delete_node",
		mcp.WithDescription(
			"Remove one node from a flow tab, and every wire pointing at it.\n\n"+
				"Cleaning up the incoming wires matters: Node-RED accepts wires aimed at "+
				"a node that no longer exists and simply never delivers to them, leaving "+
				"a flow that looks intact and quietly does less than it should. A backup "+
				"is taken before the write.",
		),
		mcp.WithString("flow_id", mcp.Required(),
			mcp.Description("The flow tab that owns the node.")),
		mcp.WithString("node_id", mcp.Required(),
			mcp.Description("The node to remove.")),
	)
	s.addWriteTool(deleteNode, s.handleDeleteNode)

	// ---- connect_nodes --------------------------------------------------
	connectNodes := mcp.NewTool("connect_nodes",
		mcp.WithDescription(
			"Wire one node's output to another node's input.\n\n"+
				"Node-RED stores connections as an array of arrays indexed by output "+
				"port, which is easy to get wrong by hand — replacing a port instead of "+
				"adding to it silently drops existing connections. This appends to the "+
				"port you name, grows the array if that port does not exist yet (a switch "+
				"node's later outputs), and does nothing if the connection already exists.\n\n"+
				"Both nodes must be in the same tab. A backup is taken before the write.",
		),
		mcp.WithString("flow_id", mcp.Required(),
			mcp.Description("The flow tab holding both nodes.")),
		mcp.WithString("from_id", mcp.Required(),
			mcp.Description("The source node, whose output is being wired.")),
		mcp.WithString("to_id", mcp.Required(),
			mcp.Description("The target node, which will receive the messages.")),
		mcp.WithNumber("port",
			mcp.Description("Output port index on the source, counting from 0. Default 0, which is the only port most nodes have.")),
	)
	s.addWriteTool(connectNodes, s.handleConnectNodes)

	// ---- delete_flow ---------------------------------------------------
	deleteFlow := mcp.NewTool("delete_flow",
		mcp.WithDescription(
			"Delete a flow tab and all its nodes. A backup of the current config "+
				"is taken first so the delete can be rolled back.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to delete.")),
	)
	s.addWriteTool(deleteFlow, s.handleDeleteFlow)

	// ---- inject_node ---------------------------------------------------
	injectNode := mcp.NewTool("inject_node",
		mcp.WithDescription(
			"Manually fire an inject node by its ID (POST /inject/:id), kicking "+
				"off a flow on demand without opening the editor.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The ID of the inject node to trigger.")),
	)
	s.addWriteTool(injectNode, s.handleInjectNode)

	// ---- list_nodes ----------------------------------------------------
	listNodes := mcp.NewTool("list_nodes",
		mcp.WithDescription(
			"List the node modules installed in the running Node-RED instance "+
				"(the palette), with their versions and enabled state.",
		),
	)
	s.addReadTool(listNodes, s.handleListNodes)

	// ---- get_node_info -------------------------------------------------
	getNodeInfo := mcp.NewTool("get_node_info",
		mcp.WithDescription(
			"Get metadata for a single installed node module by its npm package "+
				"name (e.g. \"node-red-node-mqtt\").",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("The node module name (as shown by list_nodes).")),
	)
	s.addReadTool(getNodeInfo, s.handleGetNodeInfo)

	// ---- install_node --------------------------------------------------
	installNode := mcp.NewTool("install_node",
		mcp.WithDescription(
			"Install a node module from npm into the running Node-RED instance "+
				"(adds it to the palette). MUTATES THE RUNTIME and can take a while "+
				"since npm runs under the hood. Returns the installed module info.",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("The npm package name, e.g. \"node-red-dashboard\".")),
		mcp.WithString("version",
			mcp.Description("Optional exact version to install. Latest if omitted.")),
	)
	s.addWriteTool(installNode, s.handleInstallNode)

	// ---- uninstall_node ------------------------------------------------
	uninstallNode := mcp.NewTool("uninstall_node",
		mcp.WithDescription(
			"Remove an installed node module from the palette (DELETE from the "+
				"runtime). MUTATES THE RUNTIME. Node-RED refuses to remove a module "+
				"whose nodes are still used by a flow; that error is returned as-is.",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("The module name to remove (as shown by list_nodes).")),
	)
	s.addWriteTool(uninstallNode, s.handleUninstallNode)

	// ---- enable_node ---------------------------------------------------
	enableNode := mcp.NewTool("enable_node",
		mcp.WithDescription(
			"Enable an installed node module (or a single node set within it). "+
				"MUTATES THE RUNTIME. The module stays installed either way; this "+
				"just flips its active state.",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("The module name to enable.")),
		mcp.WithString("set",
			mcp.Description("Optional node set within the module. Whole module if omitted.")),
	)
	s.addWriteTool(enableNode, s.handleEnableNode)

	// ---- disable_node --------------------------------------------------
	disableNode := mcp.NewTool("disable_node",
		mcp.WithDescription(
			"Disable an installed node module (or a single node set within it) "+
				"without uninstalling. MUTATES THE RUNTIME. Useful to debug a broken "+
				"palette by taking one module out of service.",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("The module name to disable.")),
		mcp.WithString("set",
			mcp.Description("Optional node set within the module. Whole module if omitted.")),
	)
	s.addWriteTool(disableNode, s.handleDisableNode)

	// ---- list_backups --------------------------------------------------
	listBackups := mcp.NewTool("list_backups",
		mcp.WithDescription(
			"List the flow-config backups saved before each write, newest first. "+
				"Every create/update/delete/restore takes one automatically.",
		),
	)
	s.addReadTool(listBackups, s.handleListBackups)

	// ---- diff_flows -----------------------------------------------------
	diffFlows := mcp.NewTool("diff_flows",
		mcp.WithDescription(
			"Compare two flow configurations and report what differs: which nodes were "+
				"added, removed, or changed.\n\n"+
				"Since a backup is taken before every write, this answers \"what did that "+
				"last change actually do\" without reading two full configurations. Use "+
				"\"current\" for the live configuration and a backup name from list_backups "+
				"for a snapshot; \"latest\" resolves to the most recent backup.\n\n"+
				"Comparison is by node id and semantic, so a document whose keys were "+
				"reordered is not reported as a change. Read-only.",
		),
		mcp.WithString("from", mcp.Required(),
			mcp.Description("Baseline: a backup filename, \"latest\", or \"current\".")),
		mcp.WithString("to",
			mcp.Description("What to compare against. Defaults to \"current\", the live configuration.")),
	)
	s.addReadTool(diffFlows, s.handleDiffFlows)

	// ---- restore_backup ------------------------------------------------
	restoreBackup := mcp.NewTool("restore_backup",
		mcp.WithDescription(
			"Restore the ENTIRE Node-RED flow config from a saved backup (a full "+
				"deployment that replaces everything). The current config is snapshotted "+
				"first, so a restore can itself be undone. Pass a backup name from "+
				"list_backups, or \"latest\" for the most recent.",
		),
		mcp.WithString("backup", mcp.Required(),
			mcp.Description("Backup filename (from list_backups), or \"latest\".")),
	)
	s.addWriteTool(restoreBackup, s.handleRestoreBackup)

	// ---- get_settings --------------------------------------------------
	getSettings := mcp.NewTool("get_settings",
		mcp.WithDescription(
			"Read the Node-RED server settings (adminAuth scheme, port, https, "+
				"editor theme, loaded plugins, ...). Useful for diagnosing connectivity "+
				"and configuration issues without opening the editor. Read-only.",
		),
	)
	s.addReadTool(getSettings, s.handleGetSettings)

	// ---- get_diagnostics -----------------------------------------------
	getDiagnostics := mcp.NewTool("get_diagnostics",
		mcp.WithDescription(
			"Read the Node-RED runtime diagnostics report: Node.js version and memory "+
				"usage, operating system, whether it runs in a container, locale and "+
				"timezone, and the effective settings. This is the fastest way to answer "+
				"\"what is this instance actually running on\" when diagnosing a problem. "+
				"Requires Node-RED 3.1 or later; older versions return a 404. Read-only.",
		),
	)
	s.addReadTool(getDiagnostics, s.handleGetDiagnostics)

	// ---- list_plugins ---------------------------------------------------
	listPlugins := mcp.NewTool("list_plugins",
		mcp.WithDescription(
			"List the editor plugins loaded by the runtime. Plugins extend the editor "+
				"rather than adding nodes, so they do not appear in list_nodes — this "+
				"completes the picture of what is installed. Read-only.",
		),
	)
	s.addReadTool(listPlugins, s.handleListPlugins)

	// ---- get_debug_messages ---------------------------------------------
	getDebugMessages := mcp.NewTool("get_debug_messages",
		mcp.WithDescription(
			"Read what the flows actually produced: the output of debug nodes, as it "+
				"appears in the editor's debug sidebar.\n\n"+
				"This closes the loop. After create_flow or update_flow, trigger the flow "+
				"with inject_node, then call this to see whether it did what you intended "+
				"and to read any errors it raised. Without it you can deploy a flow but "+
				"never observe it.\n\n"+
				"Collection starts with the server and runs continuously, so messages "+
				"produced before you asked are already captured. Pass since (an RFC 3339 "+
				"timestamp) to see only what arrived after a given moment — typically the "+
				"receivedAt of the last message you saw, or the time just before you "+
				"injected. The response reports the connection state and whether the "+
				"buffer overflowed, so silence is never ambiguous. Read-only.",
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum messages to return, newest last (1-200, default 50).")),
		mcp.WithString("since",
			mcp.Description("RFC 3339 timestamp, e.g. \"2026-07-27T08:15:00Z\". Only messages received after it are returned.")),
	)
	s.addReadTool(getDebugMessages, s.handleGetDebugMessages)

	// ---- get_context ----------------------------------------------------
	getContext := mcp.NewTool("get_context",
		mcp.WithDescription(
			"Read Node-RED context: the state flows keep between messages.\n\n"+
				"Context does not appear anywhere in the flow JSON, so a flow can look "+
				"completely correct and still misbehave because of a value it stored "+
				"earlier. Use this when a flow's logic reads right but its behaviour is "+
				"wrong.\n\n"+
				"Scope \"global\" is instance-wide. Scope \"flow\" needs the tab id, and "+
				"\"node\" needs the node id. Omit key to read the whole store. Values come "+
				"back keyed by store name, each with its value and type. Read-only: the "+
				"Node-RED admin API exposes no way to write context.",
		),
		mcp.WithString("scope", mcp.Required(),
			mcp.Description("\"global\", \"flow\", or \"node\"."),
			mcp.Enum("global", "flow", "node"),
		),
		mcp.WithString("id",
			mcp.Description("Flow tab id or node id. Required for scope \"flow\" and \"node\", ignored for \"global\".")),
		mcp.WithString("key",
			mcp.Description("A single context key to read. Omit to return the whole store.")),
	)
	s.addReadTool(getContext, s.handleGetContext)

	// ---- get_flows_state ----------------------------------------------
	getFlowsState := mcp.NewTool("get_flows_state",
		mcp.WithDescription(
			"Read the current runtime state of Node-RED: whether the flows are "+
				"started or stopped, plus a per-flow breakdown. Read-only.",
		),
	)
	s.addReadTool(getFlowsState, s.handleGetFlowsState)

	// ---- set_flows_state ----------------------------------------------
	setFlowsState := mcp.NewTool("set_flows_state",
		mcp.WithDescription(
			"Start or stop the Node-RED runtime (\"start\" or \"stop\"). MUTATES the "+
				"runtime: stopping pauses all flow execution, starting resumes it. A backup "+
				"of the current flow config is taken first so the change can be rolled back.",
		),
		mcp.WithString("state", mcp.Required(),
			mcp.Description("Either \"start\" or \"stop\".")),
	)
	s.addWriteTool(setFlowsState, s.handleSetFlowsState)

	// ---- set_flows -----------------------------------------------------
	// ponytail: full-deploy tool. Kept for parity with the admin API; in practice
	// prefer create_flow / update_flow / delete_flow which are scoped to one tab.
	setFlows := mcp.NewTool("set_flows",
		mcp.WithDescription(
			"Replace the ENTIRE Node-RED flow config with a supplied flow array "+
				"(full deployment). MUTATES the runtime: this is the most destructive "+
				"operation the admin API exposes. A backup of the current config is taken "+
				"first so the change can be rolled back. Prefer create_flow / update_flow "+
				"/ delete_flow for single-tab edits.",
		),
		mcp.WithString("flows", mcp.Required(),
			mcp.Description("A JSON array of flow objects (the same shape returned by list_flows).")),
	)
	s.addWriteTool(setFlows, s.handleSetFlows)

	// ---- search_nodes --------------------------------------------------
	searchNodes := mcp.NewTool("search_nodes",
		mcp.WithDescription(
			"Search the public npm registry for node-red-* modules (uses the same "+
				"endpoint as flows.nodered.org). Returns name, version, description and "+
				"link per hit. Use this before install_node when you don't know the exact "+
				"npm package name. Read-only.",
		),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("Free-text search, e.g. \"dashboard\", \"mqtt\", \"modbus\".")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum hits to return (1-50, default 10).")),
	)
	s.addReadTool(searchNodes, s.handleSearchNodes)
}

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
	slog.Debug("tool: list_flows", "detail", detail)

	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		slog.Error("list_flows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	if len(raw) == 0 {
		raw = []byte("[]")
	}

	if detail == "full" {
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
func (s *Server) handleCreateFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := flowParam(req, "flow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	flow, err := normalizeFlowDoc(raw, "", true)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
	slog.Debug("tool: update_flow", "id", id)

	if err := s.nrClient.UpdateFlow(ctx, id, flow); err != nil {
		slog.Error("update_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow %q updated (a backup was taken first).", id)), nil
}

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

// handleInjectNode triggers an inject node.
func (s *Server) handleInjectNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: inject_node", "id", id)

	if err := s.nrClient.InjectNode(ctx, id); err != nil {
		slog.Error("inject_node failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Inject node %q fired.", id)), nil
}

// handleListNodes lists the installed palette modules.
func (s *Server) handleListNodes(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_nodes")

	nodes, err := s.nrClient.ListNodes(ctx)
	if err != nil {
		slog.Error("list_nodes failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	out, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding nodes: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleGetNodeInfo returns metadata for a single node module.
func (s *Server) handleGetNodeInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: get_node_info", "module", module)

	info, err := s.nrClient.GetNodeInfo(ctx, module)
	if err != nil {
		slog.Error("get_node_info failed", "error", err, "module", module)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding node info: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleInstallNode installs a node module from npm.
func (s *Server) handleInstallNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	version := req.GetString("version", "")
	slog.Debug("tool: install_node", "module", module, "version", version)

	info, err := s.nrClient.InstallNode(ctx, module, version)
	if err != nil {
		slog.Error("install_node failed", "error", err, "module", module)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding node info: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Installed %q.\n\n```json\n%s\n```", module, string(out))), nil
}

// handleUninstallNode removes an installed node module.
func (s *Server) handleUninstallNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: uninstall_node", "module", module)

	if err := s.nrClient.UninstallNode(ctx, module); err != nil {
		slog.Error("uninstall_node failed", "error", err, "module", module)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Uninstalled %q.", module)), nil
}

// handleEnableNode enables a module or one of its node sets.
func (s *Server) handleEnableNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.setNodeEnabled(ctx, req, true)
}

// handleDisableNode disables a module or one of its node sets.
func (s *Server) handleDisableNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.setNodeEnabled(ctx, req, false)
}

// setNodeEnabled backs both enable_node and disable_node.
func (s *Server) setNodeEnabled(ctx context.Context, req mcp.CallToolRequest, enabled bool) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	set := req.GetString("set", "")
	slog.Debug("tool: set_node_enabled", "module", module, "set", set, "enabled", enabled)

	info, err := s.nrClient.SetNodeEnabled(ctx, module, set, enabled)
	if err != nil {
		slog.Error("set_node_enabled failed", "error", err, "module", module, "enabled", enabled)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding node info: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Module %q %s.\n\n```json\n%s\n```", module, verb, string(out))), nil
}

// handleListBackups lists saved flow snapshots.
func (s *Server) handleListBackups(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_backups")

	backups, err := s.nrClient.ListBackups()
	if err != nil {
		slog.Error("list_backups failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("reading backups: %v", err)), nil
	}
	if len(backups) == 0 {
		return mcp.NewToolResultText("No backups saved yet."), nil
	}
	out, err := json.MarshalIndent(backups, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding backups: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleDiffFlows compares two flow configurations.
func (s *Server) handleDiffFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to := req.GetString("to", "current")
	slog.Debug("tool: diff_flows", "from", from, "to", to)

	before, err := s.resolveFlowSource(ctx, from)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading %q: %v", from, err)), nil
	}
	after, err := s.resolveFlowSource(ctx, to)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading %q: %v", to, err)), nil
	}

	diff := nodered.DiffFlows(before, after)
	if diff.Empty() {
		return mcp.NewToolResultText(fmt.Sprintf("%q and %q are identical.", from, to)), nil
	}
	out, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding diff: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"%d difference(s) between %q and %q: %d added, %d removed, %d changed.\n\n```json\n%s\n```",
		diff.Total(), from, to, len(diff.Added), len(diff.Removed), len(diff.Changed), string(out),
	)), nil
}

// resolveFlowSource turns a diff operand into a flow configuration. "current"
// reads the live instance; anything else names a backup, with "latest" meaning
// the most recent one.
func (s *Server) resolveFlowSource(ctx context.Context, name string) (nodered.RawFlow, error) {
	if name == "current" {
		return s.nrClient.ListFlows(ctx)
	}
	if name == "latest" {
		backups, err := s.nrClient.ListBackups()
		if err != nil {
			return nil, err
		}
		if len(backups) == 0 {
			return nil, errors.New("no backups have been saved yet")
		}
		name = backups[0].Name
	}
	return s.nrClient.ReadBackup(name)
}

// handleRestoreBackup restores the full flow config from a saved backup.
func (s *Server) handleRestoreBackup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("backup")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// "latest" resolves to the most recent backup.
	if name == "latest" {
		backups, err := s.nrClient.ListBackups()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reading backups: %v", err)), nil
		}
		if len(backups) == 0 {
			return mcp.NewToolResultError("no backups to restore"), nil
		}
		name = backups[0].Name
	}
	slog.Debug("tool: restore_backup", "backup", name)

	content, err := s.nrClient.ReadBackup(name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading backup: %v", err)), nil
	}
	if err := s.nrClient.RestoreFlows(ctx, content); err != nil {
		slog.Error("restore_backup failed", "error", err, "backup", name)
		return mcp.NewToolResultError(fmt.Sprintf("restoring: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Restored flow config from %q (current state was backed up first).", name)), nil
}

// prettyJSON indents raw JSON for display, falling back to the raw bytes if
// it doesn't parse.
func prettyJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// flowParam reads a "flow" argument as either a JSON-encoded string or a
// flow object. The two shapes are equivalent on the wire (Node-RED's admin
// API wants a single flow tab as a JSON object), but accepting the object
// directly matches how MCP clients naturally describe flows.
func flowParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded flow object or a flow object passed directly", key)
		}
		return raw, nil
	case map[string]any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %v", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded flow object or a flow object passed directly", key)
	}
}

// flowsParam reads a "flows" argument as either a JSON-encoded string or a
// flow array. Same reasoning as flowParam.
func flowsParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded flow array or a flow array passed directly", key)
		}
		return raw, nil
	case []any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %v", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded flow array or a flow array passed directly", key)
	}
}

// nodeParam reads a "node" argument as either a JSON-encoded string or a
// node object. Mirrors flowParam for symmetry with the rest of the surface.
func nodeParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded node object or a node object passed directly", key)
		}
		return raw, nil
	case map[string]any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %v", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded node object or a node object passed directly", key)
	}
}

// normalizeFlowDoc turns a raw flow payload into a Node-RED admin-API
// document. If fillNodes is true and the document is a tab object missing
// its "nodes" array, an empty one is added so a POST /flow does not bounce
// with the unhelpful "missing nodes property" 400 from the runtime.
//
// The payload is accepted in any of the three shapes Node-RED hands back:
//
//   - nested tab object:  {"id":"abc","label":"x","nodes":[...]}
//   - flat array element: [{"type":"tab","id":"abc","label":"x"},
//     {"type":"inject","id":"n1","z":"abc"}, ...]
//
// The flat shape comes from GET /flows, so update_flow can take the same
// document back without forcing the caller to re-shape it. If the payload is
// a flat array, the tab id to match is taken from flowID (when supplied);
// a nested payload is accepted as-is.
func normalizeFlowDoc(raw json.RawMessage, flowID string, fillNodes bool) (nodered.RawFlow, error) {
	// Try a nested tab object first. A nested doc has either "nodes" or
	// "configs" — the two arrays Node-RED splits a tab into. A flat-array
	// element carries "type":"tab" and never both "nodes" and the rest of a
	// tab object, so the absence of those keys is the disambiguation.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		_, hasNodes := doc["nodes"]
		_, hasConfigs := doc["configs"]
		if hasNodes || hasConfigs || !looksLikeFlatArrayElement(doc) {
			if fillNodes && !hasNodes {
				doc["nodes"] = json.RawMessage(`[]`)
			}
			out, err := json.Marshal(doc)
			if err != nil {
				return nil, fmt.Errorf("re-encoding flow document: %v", err)
			}
			return nodered.RawFlow(out), nil
		}
	}

	// Flat array shape: pick the tab whose id matches flowID, then collect
	// every node whose z references it.
	var flat []json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("flow document is not a JSON object or flow array: %v", err)
	}
	if flowID == "" {
		return nil, fmt.Errorf(
			"flow document is a flat array; pass the flow id so the right tab can be picked out")
	}
	tab, nodes, configs := splitFlatFlow(flat, flowID)
	if tab == nil {
		return nil, fmt.Errorf("no tab with id %q in the supplied flat flow array", flowID)
	}

	// Project into the nested doc shape.
	out := make(map[string]json.RawMessage, len(tab)+2)
	for k, v := range tab {
		// The flat shape includes "type":"tab" and "id":"<flow>" — drop those,
		// the nested shape uses id at the top level and "tab" is implied.
		if k == "type" {
			continue
		}
		if k == "id" {
			continue
		}
		out[k] = v
	}
	if fillNodes && len(nodes) == 0 {
		nodes = []json.RawMessage{}
	}
	encodedNodes, err := json.Marshal(nodes)
	if err != nil {
		return nil, fmt.Errorf("re-encoding nodes: %v", err)
	}
	out["nodes"] = encodedNodes
	if len(configs) > 0 {
		encodedConfigs, err := json.Marshal(configs)
		if err != nil {
			return nil, fmt.Errorf("re-encoding configs: %v", err)
		}
		out["configs"] = encodedConfigs
	}
	out["id"] = json.RawMessage(fmt.Sprintf("%q", flowID))

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("re-encoding flow document: %v", err)
	}
	return nodered.RawFlow(encoded), nil
}

// splitFlatFlow partitions a flat /flows array into the tab, its nodes, and
// its config nodes, all indexed by id. Returns nil for tab when no match.
func splitFlatFlow(flat []json.RawMessage, flowID string) (tab map[string]json.RawMessage, nodes, configs []json.RawMessage) {
	for _, item := range flat {
		var meta struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Z    string `json:"z"`
		}
		if err := json.Unmarshal(item, &meta); err != nil {
			continue
		}
		switch {
		case meta.Type == "tab" && meta.ID == flowID:
			var m map[string]json.RawMessage
			if err := json.Unmarshal(item, &m); err == nil {
				tab = m
			}
		case meta.Z == flowID && meta.Type != "subflow":
			// Anything referring to the tab that is not a tab itself is a
			// child node. Config nodes (no x/y) are routed to configs so
			// the same rule Node-RED uses applies to a re-encoded flow.
			if hasXY(item) {
				nodes = append(nodes, item)
			} else {
				configs = append(configs, item)
			}
		}
	}
	return tab, nodes, configs
}

// hasXY reports whether the raw node carries x and y canvas coordinates.
func hasXY(raw json.RawMessage) bool {
	var probe struct {
		X json.RawMessage `json:"x"`
		Y json.RawMessage `json:"y"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(probe.X) > 0 && len(probe.Y) > 0
}

// looksLikeFlatArrayElement reports whether a parsed JSON object has the
// shape of one entry inside a flat GET /flows array: an id, a type, and
// no nested-array fields that would mean a full tab doc.
func looksLikeFlatArrayElement(doc map[string]json.RawMessage) bool {
	_, hasID := doc["id"]
	_, hasType := doc["type"]
	_, hasNodes := doc["nodes"]
	_, hasConfigs := doc["configs"]
	return hasID && hasType && !hasNodes && !hasConfigs
}

// normalizeFlowsArray splits a raw flows payload into the per-flow entries
// the admin API expects.
func normalizeFlowsArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var flows []json.RawMessage
	if err := json.Unmarshal(raw, &flows); err != nil {
		return nil, fmt.Errorf("flows is not a JSON array: %v", err)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("flows must contain at least one flow")
	}
	return flows, nil
}

// handleGetSettings returns the Node-RED server settings as JSON.
func (s *Server) handleGetSettings(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_settings")

	raw, err := s.nrClient.GetSettings(ctx)
	if err != nil {
		slog.Error("get_settings failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleGetDiagnostics returns the runtime diagnostics report.
func (s *Server) handleGetDiagnostics(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_diagnostics")

	raw, err := s.nrClient.GetDiagnostics(ctx)
	if err != nil {
		slog.Error("get_diagnostics failed", "error", err)
		// A 404 here almost always means an older Node-RED rather than a real
		// fault, and that is worth saying rather than leaving the model to guess.
		var apiErr *nodered.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return mcp.NewToolResultError(
				"this Node-RED does not expose /diagnostics (added in 3.1). " +
					"Use get_settings for what configuration is available instead.",
			), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleListPlugins returns the editor plugins loaded by the runtime.
func (s *Server) handleListPlugins(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_plugins")

	raw, err := s.nrClient.ListPlugins(ctx)
	if err != nil {
		slog.Error("list_plugins failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	if len(raw) == 0 || string(raw) == "[]" {
		return mcp.NewToolResultText("No editor plugins are loaded."), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

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
				"The debug stream is still connecting; nothing captured yet. Try again in a moment.",
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

// handleGetContext reads a context store, or one key within it.
func (s *Server) handleGetContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope, err := req.RequireString("scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id := req.GetString("id", "")
	key := req.GetString("key", "")
	slog.Debug("tool: get_context", "scope", scope, "id", id, "key", key)

	raw, err := s.nrClient.GetContext(ctx, scope, id, key)
	if err != nil {
		slog.Error("get_context failed", "error", err, "scope", scope, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("reading context: %v", err)), nil
	}
	// An empty store is a legitimate answer and a common one. Saying so beats
	// returning "{}" and letting the model read it as a failure.
	if s := strings.TrimSpace(string(raw)); s == "" || s == "{}" || s == `{"memory":{}}` {
		return mcp.NewToolResultText(fmt.Sprintf(
			"No context values are set for scope %q%s.", scope, describeContextTarget(id, key),
		)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// describeContextTarget renders the id/key part of a context message, so the
// empty-store reply names exactly what was looked up.
func describeContextTarget(id, key string) string {
	switch {
	case id != "" && key != "":
		return fmt.Sprintf(" (id %q, key %q)", id, key)
	case id != "":
		return fmt.Sprintf(" (id %q)", id)
	case key != "":
		return fmt.Sprintf(" (key %q)", key)
	}
	return ""
}

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
func (s *Server) handleSetFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := flowsParam(req, "flows")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	flows, err := normalizeFlowsArray(raw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: set_flows")

	if err := s.nrClient.SetFlows(ctx, flows); err != nil {
		slog.Error("set_flows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText("Flows deployed (a backup was taken first)."), nil
}

// handleSearchNodes queries the public npm registry for node-red-* modules.
func (s *Server) handleSearchNodes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// mcp-go returns a float64 for number arguments. Default to 10 when the
	// caller leaves it empty; clamp to [1,50] to keep registry responses sane.
	limit := int(req.GetFloat("limit", 10))
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	slog.Debug("tool: search_nodes", "query", query, "limit", limit)

	hits, err := s.nrClient.SearchNodes(ctx, query, limit)
	if err != nil {
		slog.Error("search_nodes failed", "error", err, "query", query)
		return mcp.NewToolResultError(fmt.Sprintf("searching npm registry: %v", err)), nil
	}
	out, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding hits: %v", err)), nil
	}
	if len(hits) == 0 {
		return mcp.NewToolResultText("No node-red modules matched that query."), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("%d hit(s) for %q.\n\n```json\n%s\n```", len(hits), query, string(out))), nil
}
