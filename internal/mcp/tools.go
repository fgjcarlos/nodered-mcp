package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// tools is the registry of MCP tool descriptors. The slice exists so the
// server initialization can log how many tools were registered.
var tools []mcp.Tool

// registerTools wires every MCP tool to its handler.
//
// Hello-world scope: only list_flows is fully implemented. The other
// tools from PLAN.md will be added in subsequent PRs to keep this
// first cut reviewable.
func (s *Server) registerTools() {
	// ---- list_flows ----------------------------------------------------
	listFlows := mcp.NewTool("list_flows",
		mcp.WithDescription(
			"List every flow (tab) in the connected Node-RED instance, "+
				"including all the nodes that belong to each flow. "+
				"Use this as the entry point when you don't know the layout of the runtime.",
		),
	)
	s.mcpServer.AddTool(listFlows, s.handleListFlows)
	tools = append(tools, listFlows)

	// ---- get_flow ------------------------------------------------------
	getFlow := mcp.NewTool("get_flow",
		mcp.WithDescription(
			"Fetch a single Node-RED flow tab by its ID, returned as its full "+
				"JSON document (tab metadata plus every node it owns).",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID (as shown by list_flows).")),
	)
	s.mcpServer.AddTool(getFlow, s.handleGetFlow)
	tools = append(tools, getFlow)

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
	s.mcpServer.AddTool(createFlow, s.handleCreateFlow)
	tools = append(tools, createFlow)

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
	s.mcpServer.AddTool(updateFlow, s.handleUpdateFlow)
	tools = append(tools, updateFlow)

	// ---- delete_flow ---------------------------------------------------
	deleteFlow := mcp.NewTool("delete_flow",
		mcp.WithDescription(
			"Delete a flow tab and all its nodes. A backup of the current config "+
				"is taken first so the delete can be rolled back.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to delete.")),
	)
	s.mcpServer.AddTool(deleteFlow, s.handleDeleteFlow)
	tools = append(tools, deleteFlow)

	// ---- inject_node ---------------------------------------------------
	injectNode := mcp.NewTool("inject_node",
		mcp.WithDescription(
			"Manually fire an inject node by its ID (POST /inject/:id), kicking "+
				"off a flow on demand without opening the editor.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The ID of the inject node to trigger.")),
	)
	s.mcpServer.AddTool(injectNode, s.handleInjectNode)
	tools = append(tools, injectNode)

	// ---- list_nodes ----------------------------------------------------
	listNodes := mcp.NewTool("list_nodes",
		mcp.WithDescription(
			"List the node modules installed in the running Node-RED instance "+
				"(the palette), with their versions and enabled state.",
		),
	)
	s.mcpServer.AddTool(listNodes, s.handleListNodes)
	tools = append(tools, listNodes)

	// ---- get_node_info -------------------------------------------------
	getNodeInfo := mcp.NewTool("get_node_info",
		mcp.WithDescription(
			"Get metadata for a single installed node module by its npm package "+
				"name (e.g. \"node-red-node-mqtt\").",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("The node module name (as shown by list_nodes).")),
	)
	s.mcpServer.AddTool(getNodeInfo, s.handleGetNodeInfo)
	tools = append(tools, getNodeInfo)

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
	s.mcpServer.AddTool(installNode, s.handleInstallNode)
	tools = append(tools, installNode)

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
	s.mcpServer.AddTool(uninstallNode, s.handleUninstallNode)
	tools = append(tools, uninstallNode)

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
	s.mcpServer.AddTool(enableNode, s.handleEnableNode)
	tools = append(tools, enableNode)

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
	s.mcpServer.AddTool(disableNode, s.handleDisableNode)
	tools = append(tools, disableNode)

	// ---- list_backups --------------------------------------------------
	listBackups := mcp.NewTool("list_backups",
		mcp.WithDescription(
			"List the flow-config backups saved before each write, newest first. "+
				"Every create/update/delete/restore takes one automatically.",
		),
	)
	s.mcpServer.AddTool(listBackups, s.handleListBackups)
	tools = append(tools, listBackups)

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
	s.mcpServer.AddTool(restoreBackup, s.handleRestoreBackup)
	tools = append(tools, restoreBackup)

	// ---- get_settings --------------------------------------------------
	getSettings := mcp.NewTool("get_settings",
		mcp.WithDescription(
			"Read the Node-RED server settings (adminAuth scheme, port, https, "+
				"editor theme, loaded plugins, ...). Useful for diagnosing connectivity "+
				"and configuration issues without opening the editor. Read-only.",
		),
	)
	s.mcpServer.AddTool(getSettings, s.handleGetSettings)
	tools = append(tools, getSettings)

	// ---- get_flows_state ----------------------------------------------
	getFlowsState := mcp.NewTool("get_flows_state",
		mcp.WithDescription(
			"Read the current runtime state of Node-RED: whether the flows are "+
				"started or stopped, plus a per-flow breakdown. Read-only.",
		),
	)
	s.mcpServer.AddTool(getFlowsState, s.handleGetFlowsState)
	tools = append(tools, getFlowsState)

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
	s.mcpServer.AddTool(setFlowsState, s.handleSetFlowsState)
	tools = append(tools, setFlowsState)

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
	s.mcpServer.AddTool(setFlows, s.handleSetFlows)
	tools = append(tools, setFlows)

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
	s.mcpServer.AddTool(searchNodes, s.handleSearchNodes)
	tools = append(tools, searchNodes)
}

// handleListFlows returns the current flows as a JSON document.
func (s *Server) handleListFlows(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_flows")

	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		slog.Error("list_flows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	if len(raw) == 0 {
		raw = []byte("[]")
	}

	summary := fmt.Sprintf(
		"Found %d flow tab(s) in Node-RED.\n\n```json\n%s\n```",
		nodered.FlowTabCount(raw), prettyJSON(raw),
	)
	return mcp.NewToolResultText(summary), nil
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

// handleCreateFlow creates a new flow tab from a JSON document.
func (s *Server) handleCreateFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowStr, err := req.RequireString("flow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: create_flow")

	created, err := s.nrClient.CreateFlow(ctx, nodered.RawFlow(flowStr))
	if err != nil {
		slog.Error("create_flow failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow created.\n\n```json\n%s\n```", prettyJSON(created))), nil
}

// handleUpdateFlow replaces an existing flow tab with a new JSON document.
func (s *Server) handleUpdateFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	flowStr, err := req.RequireString("flow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: update_flow", "id", id)

	if err := s.nrClient.UpdateFlow(ctx, id, nodered.RawFlow(flowStr)); err != nil {
		slog.Error("update_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Flow %q updated (a backup was taken first).", id)), nil
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
	summary := fmt.Sprintf("%d node module(s) installed.\n\n```json\n%s\n```", len(nodes), string(out))
	return mcp.NewToolResultText(summary), nil
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
	summary := fmt.Sprintf("%d backup(s), newest first.\n\n```json\n%s\n```", len(backups), string(out))
	return mcp.NewToolResultText(summary), nil
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

// handleGetFlowsState returns the current runtime state of Node-RED.
func (s *Server) handleGetFlowsState(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_flows_state")

	raw, err := s.nrClient.GetFlowsState(ctx)
	if err != nil {
		slog.Error("get_flows_state failed", "error", err)
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
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Node-RED runtime %sed (a backup was taken first).", state)), nil
}

// handleSetFlows replaces the entire flow config with a full deployment.
func (s *Server) handleSetFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowsStr, err := req.RequireString("flows")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: set_flows")

	var flows []json.RawMessage
	if err := json.Unmarshal([]byte(flowsStr), &flows); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("flows is not a valid JSON array: %v", err)), nil
	}
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
