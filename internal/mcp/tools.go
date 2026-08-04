// Error convention: fmt.Errorf("<verb> <noun>: %w", err) for wrapped errors; %v only for non-error interpolation.

package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
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
				"that response is very large.\n\n"+
				"### Working with large instances\n\n"+
				"When detail=\"full\" and the instance has more nodes than the configured "+
				"threshold (default 200), the tool returns a warning instead of the full "+
				"payload to protect the model context window. Pass force=true to override "+
				"the guard, or use get_flow to fetch one tab at a time.\n\n"+
				"Note: Node-RED redacts credential values in API responses; credential "+
				"fields are present but their values are always empty strings.",
		),
		mcp.WithString("detail",
			mcp.Description("\"summary\" (default) for the compact map, or \"full\" for the entire raw flow config."),
			mcp.Enum("summary", "full"),
		),
		mcp.WithBoolean("force",
			mcp.Description("Set to true to bypass the node-count safeguard when using detail=\"full\". Use with caution on large deployments.")),
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
				"JSON document (tab metadata plus every node it owns). "+
				"Note: Node-RED redacts credential values in API responses; credential "+
				"fields are present but their values are always empty strings.",
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
				"the ID and returns the created flow. A backup is taken first.\n\n"+
				"WARNING: this tool can deploy any node type the Node-RED instance has "+
				"installed, including exec/system nodes that execute shell commands on "+
				"the Node-RED host. The MCP server blocks a configurable set of node "+
				"types (MCP_NODE_DENYLIST, default: exec,system); see SECURITY.md.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("flow", mcp.Required(),
			mcp.Description("The flow document as a JSON string.")),
	)
	s.addWriteTool(createFlow, s.handleCreateFlow)

	// ---- export_flow / import_flow ------------------------------------
	// Editor clipboard model exposed as MCP tools. export_flow returns
	// the same shape Ctrl+C produces in the editor; import_flow takes
	// that shape back and creates a new flow tab on this instance.
	exportFlow := mcp.NewTool("export_flow",
		mcp.WithDescription(
			"Read a flow tab in editor clipboard format. Returns the same JSON "+
				"document that Ctrl+C produces in the Node-RED editor — a single-"+
				"element array whose element is the flow tab. Use this to capture a "+
				"tested flow, then call import_flow on another instance to deploy it. "+
				"Wires round-trip intact. Read-only.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to export.")),
	)
	s.addReadTool(exportFlow, s.handleExportFlow)

	importFlow := mcp.NewTool("import_flow",
		mcp.WithDescription(
			"Create a new flow tab from an editor clipboard JSON document. The "+
				"argument shape is the same one export_flow returns: a single-element "+
				"array whose element is the flow tab. Only one tab per call — split "+
				"multi-tab pastes by hand. Returns the runtime-assigned id of the new tab.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("clipboard", mcp.Required(),
			mcp.Description("Editor clipboard JSON: a single-element array containing the flow tab. Either the raw array or a JSON-encoded string is accepted.")),
	)
	s.addWriteTool(importFlow, s.handleImportFlow)

	// ---- update_flow ---------------------------------------------------
	updateFlow := mcp.NewTool("update_flow",
		mcp.WithDescription(
			"Replace an existing flow tab with a new JSON document (PUT semantics: "+
				"the whole flow is replaced). Read the flow first with get_flow, modify "+
				"it, and send it back intact — every node field is preserved.\n\n"+
				"WARNING: this tool can deploy any node type, including exec/system "+
				"nodes that execute shell commands on the Node-RED host. The MCP "+
				"server blocks a configurable set of node types (MCP_NODE_DENYLIST, "+
				"default: exec,system); see SECURITY.md.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
				"appends and leaves the rest of the tab byte-for-byte identical. The "+
				"wires are validated before the write.\n\n"+
				"The node needs at least an id and a type. Wire it up afterwards with "+
				"connect_nodes rather than hand-writing the wires array.\n\n"+
				"WARNING: this tool can deploy any node type, including exec/system "+
				"nodes that execute shell commands on the Node-RED host. The MCP "+
				"server blocks a configurable set of node types (MCP_NODE_DENYLIST, "+
				"default: exec,system); see SECURITY.md.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
				"A node's id cannot be changed, because the wires reference it. The "+
				"wires are validated before the write.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
				"a flow that looks intact and quietly does less than it should.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
				"Both nodes must be in the same tab.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
			"Delete a flow tab and all its nodes.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to delete.")),
	)
	s.addWriteTool(deleteFlow, s.handleDeleteFlow)

	// ---- validate_flow -------------------------------------------------
	// validate_flow runs the same structural checks the write path applies
	// (dangling wires, duplicate / missing ids, missing x/y) against an
	// arbitrary flow document, WITHOUT writing it. The caller formats the
	// returned issues list so a model can see "would this break?" before
	// committing. Read-only: no HTTP, no state change.
	validateFlow := mcp.NewTool("validate_flow",
		mcp.WithDescription(
			"Run the structural checks the write path applies (dangling wires, "+
				"duplicate or missing ids, missing x/y canvas coordinates) against a "+
				"flow document WITHOUT writing it. Returns every issue found so the "+
				"caller can fix them all at once, rather than discover one per failed "+
				"deploy. Read-only — nothing is sent to the runtime.",
		),
		mcp.WithString("flow", mcp.Required(),
			mcp.Description("The flow document as a JSON string (same shape as create_flow).")),
	)
	s.addReadTool(validateFlow, s.handleValidateFlow)

	// ---- disable_flow / enable_flow ------------------------------------
	// Flips the "disabled" flag on an existing flow tab via the same
	// editFlow read-modify-write path the granular node tools use. Goes
	// through snapshot + wire validation + writeMu, so concurrent edits
	// cannot race a disable against a node mutation. Both are write
	// tools: disabling a tab stops it from running, and Node-RED's editor
	// does not surface any undo.
	disableFlow := mcp.NewTool("disable_flow",
		mcp.WithDescription(
			"Stop a flow tab from running without deleting it. Flips the tab's "+
				"\"disabled\" flag via PUT /flow/:id. The tab stays in the editor "+
				"and can be re-enabled with enable_flow.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to disable (as shown by list_flows).")),
	)
	s.addWriteTool(disableFlow, s.handleDisableFlow)

	enableFlow := mcp.NewTool("enable_flow",
		mcp.WithDescription(
			"Re-enable a previously disabled flow tab. Flips the tab's "+
				"\"disabled\" flag back to false via PUT /flow/:id. The tab starts "+
				"running again with whatever nodes were already on it — no redeploy needed.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The flow tab ID to enable (as shown by list_flows).")),
	)
	s.addWriteTool(enableFlow, s.handleEnableFlow)

	// ---- subflow CRUD --------------------------------------------------
	// Subflow definitions live in /flow/global (the admin API does not
	// expose them through /flow/:id). All six tools go through the same
	// nodered.Client helpers which handle the fetch-modify-PUT dance and
	// the wire/backup guards. The descriptions call this out so a
	// caller that wants to know why there is no direct endpoint does
	// not have to read the source.
	listSubflows := mcp.NewTool("list_subflows",
		mcp.WithDescription(
			"List every subflow definition currently installed in the "+
				"runtime, as opaque JSON.\n\n"+
				"Subflow definitions are not flow tabs: they live in a separate "+
				"collection on the runtime, are not addressable by /flow/:id, "+
				"and are not shown by list_flows (which only returns tabs and "+
				"their contents). Use this to discover what reusable subflows "+
				"the instance knows about, then follow up with get_subflow to "+
				"read one, or instantiate_subflow to drop an instance into a tab. "+
				"Read-only.",
		),
	)
	s.addReadTool(listSubflows, s.handleListSubflows)

	getSubflow := mcp.NewTool("get_subflow",
		mcp.WithDescription(
			"Fetch a single subflow definition by ID, returned as its full "+
				"JSON document (the subflow metadata, in/out port descriptors, "+
				"environment variables, and every node the subflow contains). "+
				"Read-only.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The subflow definition ID (as shown by list_subflows).")),
	)
	s.addReadTool(getSubflow, s.handleGetSubflow)

	createSubflow := mcp.NewTool("create_subflow",
		mcp.WithDescription(
			"Install a new subflow definition. The runtime's subflow collection "+
				"is replaced wholesale, so this tool reads the current set, "+
				"appends the new one, and writes the lot back.\n\n"+
				"Pass the full subflow JSON object: at minimum {\"id\":\"...\", "+
				"\"type\":\"subflow\", \"name\":\"...\"} with a nodes array "+
				"describing the internal logic. Fails with a clear error if a "+
				"subflow with the same id already exists; use update_subflow to "+
				"change an existing one.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("subflow", mcp.Required(),
			mcp.Description("The subflow definition as a JSON object or JSON-encoded string.")),
	)
	s.addWriteTool(createSubflow, s.handleCreateSubflow)

	updateSubflow := mcp.NewTool("update_subflow",
		mcp.WithDescription(
			"Replace an existing subflow definition. The runtime's subflow "+
				"collection is replaced wholesale, so this tool reads the "+
				"current set, swaps the named entry, and writes the lot back. "+
				"Fails with a 404-style error if no subflow with the given id "+
				"exists; use create_subflow for new ones.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The subflow definition ID to replace.")),
		mcp.WithString("subflow", mcp.Required(),
			mcp.Description("The replacement subflow definition as a JSON object or JSON-encoded string. The id field must match the path id.")),
	)
	s.addWriteTool(updateSubflow, s.handleUpdateSubflow)

	deleteSubflow := mcp.NewTool("delete_subflow",
		mcp.WithDescription(
			"Remove a subflow definition. Fails with a 404-style error if no subflow with "+
				"the given id exists.\n\n"+
				"Caveat: the runtime does not check that no instance of the "+
				"subflow is in use before removing the definition. Any "+
				"instance nodes left in flow tabs will point at a missing "+
				"subflow after the next deploy — the same behaviour as the "+
				"editor, where the operator is expected to be aware.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The subflow definition ID to remove.")),
	)
	s.addWriteTool(deleteSubflow, s.handleDeleteSubflow)

	instantiateSubflow := mcp.NewTool("instantiate_subflow",
		mcp.WithDescription(
			"Add a new instance of a subflow to a flow tab.\n\n"+
				"The new node's type is set to \"subflow:<id>\" — the format the "+
				"runtime and the editor both expect — and its z (owning tab) is "+
				"set to flow_id. Other instance properties (name, x, y, wires, "+
				"env, custom keys) are taken from the params argument verbatim, "+
				"so callers can pass instance-level overrides without the MCP "+
				"having to know about them.\n\n"+
				"The wires are validated like any other node add. If flow_id does "+
				"not exist, or subflow_id is not a known definition, the call fails "+
				"before anything is written. Read /list_subflows first to discover "+
				"available subflow ids, and /get_flow first to find tab ids.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
		),
		mcp.WithString("flow_id", mcp.Required(),
			mcp.Description("The flow tab to add the instance to (from list_flows or search_flows).")),
		mcp.WithString("subflow_id", mcp.Required(),
			mcp.Description("The subflow definition to instantiate (from list_subflows).")),
		mcp.WithObject("params",
			mcp.Description("Optional instance overrides, e.g. {\"id\":\"inst1\",\"name\":\"first\",\"x\":200,\"y\":100,\"wires\":[[\"n2\"]],\"env\":[{\"name\":\"foo\",\"value\":\"bar\"}]}. A JSON-encoded string is also accepted."),
			mcp.AdditionalProperties(true),
		),
	)
	s.addWriteTool(instantiateSubflow, s.handleInstantiateSubflow)

	// ---- inject_node ---------------------------------------------------
	injectNode := mcp.NewTool("inject_node",
		mcp.WithDescription(
			"WARNING: executes the live Node-RED flow and may trigger real external "+
				"side effects (HTTP requests, MQTT publishes, database writes, email, etc.). "+
				"Do not use on production flows without operator confirmation.\n\n"+
				"Manually fire an inject node by its ID (POST /inject/:id), kicking "+
				"off a flow on demand without opening the editor.\n\n"+
				"By default the inject fires with whatever the node was configured "+
				"to send (its payload, topic, etc.). Pass `payload` to override "+
				"the message: the runtime forwards the body to node.receive when "+
				"it carries the magic __user_inject_props__ trigger, so an "+
				"empty array there means \"use this body as msg\" — perfect for "+
				"commissioning a flow with a specific input (\"what happens if "+
				"msg.payload = 'foo'?\"). The payload is sent as a JSON object "+
				"or JSON-encoded string; anything json.Unmarshal accepts.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("The ID of the inject node to trigger.")),
		mcp.WithObject("payload",
			mcp.Description("Optional. The msg payload to send through the inject. A JSON object (e.g. {\"foo\":1}) or a JSON-encoded string (e.g. \"42\" or \"[1,2,3]\"). Omit to fire the inject with its configured payload."),
			mcp.AdditionalProperties(true),
		),
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
				"first, so a restore can itself be undone. Use list_backups to find "+
				"available snapshots; pass the backup filename or \"latest\" for the most recent.",
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
				"Node-RED admin API exposes no way to write context; use set_context for that.",
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

	// ---- set_context ----------------------------------------------------
	setContext := mcp.NewTool("set_context",
		mcp.WithDescription(
			"Write to Node-RED context: the state flows keep between messages.\n\n"+
				"The admin API exposes no way to write context (it only reads it via get_context), "+
				"so this tool installs a small helper on the runtime on first use: a single flow tab "+
				"named \"__mcp_context_helper__\" containing one inject node wired to one function "+
				"node. Each call dispatches a payload to that helper. The helper is reused across "+
				"calls; it is not re-created per invocation.\n\n"+
				"Scope \"global\" is instance-wide. Scope \"flow\" writes to the helper's own "+
				"flow context (Node-RED has no runtime API to write another tab's flow context "+
				"from a single function node). Scope \"node\" writes to the helper's function "+
				"node's own context — pass the helper's function id (visible via list_flows as "+
				"\"__mcp_context_helper_set__\") to get_context to read it back. The \"id\" arg "+
				"is required for scope \"flow\" and \"node\" and must match the helper's runtime id — "+
				"call list_flows to find it (look for the tab labelled \"__mcp_context_helper__\").\n\n"+
				"Pass \"value\" as a JSON-encoded string (objects, arrays, numbers, booleans, "+
				"strings, null — all accepted). It is parsed server-side and stored as the "+
				"corresponding JS value in the context store.\n\n"+
				"To write multiple keys, call set_context once per key (the issue's out-of-scope "+
				"item; we do not batch). Read back what you wrote with get_context.",
		),
		mcp.WithString("scope", mcp.Required(),
			mcp.Description("\"global\", \"flow\", or \"node\"."),
			mcp.Enum("global", "flow", "node"),
		),
		mcp.WithString("id",
			mcp.Description("Flow tab id (for scope \"flow\") or node id (for scope \"node\"). Required for non-global scopes; ignored for \"global\".")),
		mcp.WithString("key", mcp.Required(),
			mcp.Description("The context key to set.")),
		mcp.WithString("value", mcp.Required(),
			mcp.Description(`The value to store, as a JSON-encoded string: "42", "\"hello\"", "[1,2,3]", "{\"a\":1}", "true", "null". Anything json.Unmarshal accepts.`)),
	)
	s.addWriteTool(setContext, s.handleSetContext)

	// ---- get_flows_state ----------------------------------------------
	getFlowsState := mcp.NewTool("get_flows_state",
		mcp.WithDescription(
			// Node-RED 5.x GET /flows/state returns only {"state":"start"} or
			// {"state":"stop"} — no per-flow detail. Node-RED may add per-flow
			// state in a future version; update this description when that ships.
			"Read the current runtime state of Node-RED: whether the flows are "+
				"started or stopped. Node-RED 5.x returns only "+
				"{\"state\":\"start\"} or {\"state\":\"stop\"} — no per-flow detail. Read-only.",
		),
	)
	s.addReadTool(getFlowsState, s.handleGetFlowsState)

	// ---- get_runtime_logs --------------------------------------------
	getRuntimeLogs := mcp.NewTool("get_runtime_logs",
		mcp.WithDescription(
			"Read what the Node-RED runtime itself printed: boot messages, deploy "+
				"errors, module-load failures. The flow config can look correct and "+
				"still produce nothing, and the runtime log is the only place that "+
				"answers \"what did Node-RED think happened\" when a deploy fails "+
				"silently.\n\n"+
				"Filters: pass `level` (\"info\" | \"warn\" | \"error\") to drop the "+
				"noise, and pass `since` as either an RFC 3339 timestamp (e.g. "+
				"\"2026-07-28T19:00:00Z\") for everything after that moment, or as "+
				"a line offset \"-N\" for the last N lines. The default returns the "+
				"100 most recent lines, capped at 1000.\n\n"+
				"Caveat: stock Node-RED 5.x does not expose a `/logs` admin endpoint, "+
				"so a 404 here is the expected answer today. The tool surfaces that as "+
				"a clear operator hint, not a generic HTTP error. Read-only.",
		),
		mcp.WithString("since",
			mcp.Description("Either an RFC 3339 timestamp (e.g. \"2026-07-28T19:00:00Z\") for entries at or after that time, or \"-N\" for the last N lines.")),
		mcp.WithString("level",
			mcp.Description("Filter by level: \"info\", \"warn\", or \"error\". Default: all levels."),
			mcp.Enum("info", "warn", "error")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum lines to return (1-1000, default 100).")),
	)
	s.addReadTool(getRuntimeLogs, s.handleGetRuntimeLogs)

	// ---- get_node_status ----------------------------------------------
	getNodeStatus := mcp.NewTool("get_node_status",
		mcp.WithDescription(
			"Read the current runtime status of a Node-RED node, the same status "+
				"shown by the coloured dot under each node in the editor: connected, "+
				"reconnecting, errored, or disconnected.\n\n"+
				"This closes the loop on \"is this node actually doing anything\": a "+
				"node can be in the flow config and even receive messages, and still "+
				"be errored (a broker that has gone away, a parse error, a timeout). "+
				"Pass `node_id` for one node, or `flow_id` for every node in a tab.\n\n"+
				"The response carries the most recent status and, for an errored node, "+
				"the last error text. The cache is fed by the /comms WebSocket, which "+
				"is opt-in via `MCP_DEBUG_STREAM=on` — when the flag is off, the tool "+
				"reports a clear \"stream unavailable\" message rather than returning "+
				"stale data. Read-only: querying does not trigger any runtime side "+
				"effect.",
		),
		mcp.WithString("node_id",
			mcp.Description("The node to look up. Mutually exclusive with flow_id.")),
		mcp.WithString("flow_id",
			mcp.Description("The flow tab whose nodes you want to enumerate. The tool resolves the flow's node ids and joins them with the status cache; nodes in the flow but never seen by the tail are reported as \"unknown\".")),
	)
	s.addReadTool(getNodeStatus, s.handleGetNodeStatus)

	// ---- set_flows_state ----------------------------------------------
	setFlowsState := mcp.NewTool("set_flows_state",
		mcp.WithDescription(
			"Start or stop the Node-RED runtime (\"start\" or \"stop\"). MUTATES the "+
				"runtime: stopping pauses all flow execution, starting resumes it.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
				"operation the admin API exposes. Prefer create_flow / update_flow "+
				"/ delete_flow for single-tab edits.\n\n"+
				"WARNING: this tool can deploy any node type, including exec/system "+
				"nodes that execute shell commands on the Node-RED host. The MCP "+
				"server blocks a configurable set of node types (MCP_NODE_DENYLIST, "+
				"default: exec,system); see SECURITY.md.\n\n"+
				"A backup of the current configuration is taken automatically before the "+
				"write and can be restored with restore_backup.",
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
				"npm package name. Read-only. "+
				"Requires outbound network access to the npm registry "+
				"(https://registry.npmjs.org or NODERED_SEARCH_BASE_URL if set).",
		),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("Free-text search, e.g. \"dashboard\", \"mqtt\", \"modbus\".")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum hits to return (1-50, default 10).")),
	)
	s.addReadTool(searchNodes, s.handleSearchNodes)

	// ---- get_runtime_info ---------------------------------------------
	// Companion to get_diagnostics: that tool returns Node-RED's own
	// runtime report (memory, OS, runtime state); this one answers
	// "what can this MCP actually do here?" by combining the
	// detected NR version, the runtime-state gate, and the debug
	// stream flag into a per-tool capability matrix. Read-only.
	runtimeInfo := mcp.NewTool("get_runtime_info",
		mcp.WithDescription(
			"Report the MCP server's view of the connected Node-RED "+
				"runtime: detected Node-RED version, the runtime-state "+
				"gate, the debug-stream setting, and a per-tool capability "+
				"matrix classifying each registered tool as ok, "+
				"version_too_low, endpoint_not_mounted, setting_disabled, "+
				"stream_disabled, or unknown.\n\n"+
				"This is the single tool to call when an LLM session starts "+
				"on an unfamiliar runtime: it tells the model which tools "+
				"will work and which will fail before the first call is made. "+
				"\n\n"+
				"Companion to get_diagnostics (which returns Node-RED's own "+
				"report); this tool returns the MCP server's report. Read-only.",
		),
	)
	s.addReadTool(runtimeInfo, s.handleGetRuntimeInfo)
}
