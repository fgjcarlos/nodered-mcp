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

## Node-type policy

Every write tool that can carry a node type is subject to the
`MCP_NODE_DENYLIST` policy (default `exec,system`). The policy is
judged on the effective configuration the write would deploy, and the
document is walked recursively, so a forbidden type nested inside a
subflow definition is refused too. `update_node` is judged on the node
as it is after the patch is merged. The refusal names
`MCP_NODE_DENYLIST` and the offending type, and it happens before the
runtime is contacted and before any backup snapshot is taken.

Covered surfaces: `create_flow`, `update_flow`, `add_node`,
`update_node`, `connect_nodes`, `import_flow`, `set_flows`,
`restore_backup`, `create_subflow`, `update_subflow`,
`instantiate_subflow`. The server's own `set_context` helper obeys the
same policy. See [`SECURITY.md`](../SECURITY.md).

## Retry safety

Read tools retry typed HTTP transport failures up to twice (three attempts
in total), with a 15-second deadline per attempt. Validation errors and
complete HTTP error responses are not retried, regardless of their text.

Write and action tools run once. If a connection closes or a deadline
expires without a complete response, the tool reports an **indeterminate
outcome**: the action may already have executed. Inspect the Node-RED runtime
before repeating the operation; a final error does not prove nothing changed.
Backups protect configuration recovery, not against duplicate messages or
hardware commands. This policy governs automatic MCP handler replays; the
existing `set_context` retry after an explicit provisioning-related HTTP 404
is unchanged.

## Backup recovery limits

Automatic backups are **structural snapshots of `GET /flows`**, not full
Node-RED disaster-recovery backups. `restore_backup` restores flow JSON only.
It does not back up or recover Node-RED's separate encrypted credential store
or the keys/configuration needed to decrypt it. The Admin API does not expose
hidden secrets for this server to export.

**Before deleting nodes or tabs, or replacing flows**, arrange authorized
external recovery. Node-RED can remove their credentials when they are deleted;
restoring the same node IDs from a snapshot cannot reconstruct those secrets.
For example, restoring a deleted MQTT broker may recover the node but not its
username/password. A successful `restore_backup` does not prove authentication
or runtime operation has been recovered. Restoring a snapshot can itself remove
nodes and credentials; the automatic pre-restore snapshot is structural too.

### External recovery procedure

1. Before the destructive operation, stop Node-RED and take a consistent,
   authorized backup of its user directory. Include the matching flow JSON,
   encrypted credential file (`flows_cred.json`, or the equivalent for the
   configured flow file), `settings.js`, and `.config.runtime.json` when it
   contains the generated encryption key. Preserve the original
   `credentialSecret` and any externally supplied secret/configuration as well.
   Protect this backup as sensitive material; do not send it through MCP.
2. Restart Node-RED after the backup. For full recovery later, stop it again
   and restore the matching external files with their original encryption
   key/configuration, then restart. This is an operator-managed recovery,
   **not** something `restore_backup` performs.
3. If no matching credential backup and key exist, restore the structure,
   re-enter the credentials in the Node-RED editor, and deploy. Check the
   affected nodes' authentication and operation before declaring recovery done.

The write tools advertise these limits before execution, and `restore_backup`
repeats them in its successful response. No additional filesystem or secret
access is granted to the MCP server.

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
