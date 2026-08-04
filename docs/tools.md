# Tools

43 tools, 3 resources, 2 prompts. Tools are classified by risk:

- **read** — no side effects.
- **write** — mutates persisted configuration and takes a backup first.
- **action** — has a runtime side effect that is not persisted (e.g. `inject_node`).

The `read` / `write` split is enforced at tool **registration**, not
inside each handler: the 23 mutating tools are not advertised when
`--read-only` is set, so a model cannot call what it cannot see.
`inject_node` counts as mutating — firing an inject can send a real
command to real hardware.

The 20 tools marked `read` are the only ones registered under
`--read-only`.

## Flows

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | Summary by default, `detail="full"` opt-in |
| `search_flows` | `GET /flows` | read | |
| `get_flow` | `GET /flow/:id` | read | |
| `create_flow` | `POST /flow` | write | |
| `update_flow` | `PUT /flow/:id` | write | Full rewrite; prefer the granular tools |
| `delete_flow` | `DELETE /flow/:id` | write | |
| `set_flows` | `POST /flows` | write | Full deploy, most destructive |
| `add_node` | `PUT /flow/:id` | write | |
| `update_node` | `PUT /flow/:id` | write | Merges properties |
| `delete_node` | `PUT /flow/:id` | write | Cleans incoming wires |
| `connect_nodes` | `PUT /flow/:id` | write | |
| `validate_flow` | local | read | Dry-run structural check |
| `disable_flow` | `PUT /flow/:id` | write | |
| `enable_flow` | `PUT /flow/:id` | write | |
| `inject_node` | `POST /inject/:id` | action | Excluded from `--read-only`; optional payload |
| `export_flow` | `GET /flow/:id` | read | |
| `import_flow` | `POST /flow` | write | |
| `list_subflows` | `GET /flow/global` | read | |
| `get_subflow` | `GET /flow/global` | read | |
| `create_subflow` | `PUT /flow/global` | write | |
| `update_subflow` | `PUT /flow/global` | write | |
| `delete_subflow` | `PUT /flow/global` | write | |
| `instantiate_subflow` | `PUT /flow/:id` | write | |

## Palette

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `list_nodes` | `GET /nodes` | read | |
| `get_node_info` | `GET /nodes/:module` | read | |
| `search_nodes` | npm registry | read | Private mirror via `Options.SearchBaseURL` |
| `install_node` | `POST /nodes` | write | |
| `uninstall_node` | `DELETE /nodes/:module` | write | |
| `enable_node` | `PUT /nodes/:module[/:set]` | write | |
| `disable_node` | `PUT /nodes/:module[/:set]` | write | |

## Runtime, diagnostics, recovery

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | |
| `get_diagnostics` | `GET /diagnostics` | read | Requires Node-RED ≥3.1 |
| `get_flows_state` | `GET /flows/state` | read | |
| `get_context` | `GET /context/...` | read | editor-api, no stability contract |
| `set_context` | `POST /context/...` | write | |
| `get_debug_messages` | `/comms` WebSocket | read | Buffer of 500, reconnects |
| `get_runtime_logs` | journal / stream | read | |
| `list_plugins` | `GET /plugins` | read | editor-api |
| `get_runtime_info` | companion to `get_diagnostics` | read | MCP server view of the runtime |
| `get_node_status` | `/comms` WebSocket | read | |
| `set_flows_state` | `POST /flows/state` | write | |
| `list_backups` | local | read | |
| `diff_flows` | local + `GET /flows` | read | |
| `restore_backup` | `POST /flows` | write | |

## Resources (3)

| URI | Description |
|---|---|
| `nodered://flows/current` | The full current flow configuration |
| `nodered://settings` | Server settings |
| `nodered://flows/state` | Runtime state |

## Prompts (2)

| Name | Description |
|---|---|
| `explain_flow` | Describe what a flow does, its triggers, and external dependencies |
| `generate_flow` | Build a flow from a plain-English description |