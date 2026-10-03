# Tools

44 tools, 3 resources, 2 prompts. Tools are classified by risk:

- **read** — no side effects.
- **write** — mutates persisted configuration and takes a backup first.
- **action** — has a runtime side effect that is not persisted (e.g. `inject_node`).

The `read` / `write` split is enforced at tool **registration**, not
inside each handler: the 23 mutating tools are not advertised when
`--read-only` is set, so a model cannot call what it cannot see.
`inject_node` counts as mutating — firing an inject can send a real
command to real hardware.

The 21 tools marked `read` are the only ones registered under
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
## Reading `get_runtime_info`

The response carries two maps. `capabilityMatrix` is the state of every
tool against the runtime this MCP is connected to; `capabilityGuidance`
explains the non-`ok` ones.

```json
{
  "mcp": {
    "capabilityMatrix": {
      "set_context": "version_too_low",
      "get_diagnostics": "ok",
      "get_flows_state": "setting_disabled"
    },
    "capabilityGuidance": {
      "set_context": {
        "reason": "Node-RED 3.0.0 is below the 5.0.0 minimum required by set_context",
        "remedy": "Upgrade Node-RED to at least 5.0.0"
      },
      "get_flows_state": {
        "reason": "the runtime-state setting did not read as enabled; settings.runtimeState.enabled was false, absent, or /settings was unreadable",
        "remedy": "Set settings.runtimeState.enabled to true in settings.js (or via the runtime settings UI) and restart Node-RED"
      }
    }
  }
}
```

`ok` entries have **no** guidance entry — absence is the signal that the
tool works, so a client only has to read the map when something is
degraded.

`unknown` is a real state, not a euphemism: it means the Node-RED version
could not be detected, so the capability is genuinely undetermined. It
carries a reason and deliberately **no** remedy, because there is no
known fix to recommend.

Every reason is derived from a probe the handler already ran. Nothing
here is inferred, and no mutating endpoint is touched to produce it. The
version reason names the actual minimum for that specific tool, so
`set_context` (needs 5.0.0) and `get_diagnostics` (needs 3.1.0) give
different advice.

Some states have more than one possible cause and the probes cannot tell
them apart, so the reason states the observation rather than a verdict.
`setting_disabled` covers an operator-closed gate, a missing setting
key, and an unreadable `/settings` alike. `stream_disabled` is reported
without naming a cause, because the capability is classified regardless
of the `MCP_DEBUG_STREAM` flag — the remedy is the flag, not a
diagnosis.
