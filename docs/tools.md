# Tools

**English** | [Español](es/tools.md)

This page is the catalogue of everything the server lets an AI assistant do
with your Node-RED: which [tools](glossary.md#tool) exist, which of them only
look and which change things, and the safety rules that apply to the ones that
change things. You need it if you want to know what to ask your assistant for,
if you run the server in [read-only mode](glossary.md#read-only-mode), or if
you have to decide whether a particular operation is safe to allow.

## Contents

- [How to read this page](#how-to-read-this-page)
- [Node-type policy](#node-type-policy)
- [Retry safety](#retry-safety)
- [Timeout budgets](#timeout-budgets)
- [Backup recovery limits](#backup-recovery-limits)
- [Flows](#flows)
- [Palette](#palette)
- [Runtime, diagnostics, recovery](#runtime-diagnostics-recovery)
- [Context helper recovery](#context-helper-recovery)
- [Resources (3)](#resources-3)
- [Prompts (2)](#prompts-2)
- [Node-RED version detection](#node-red-version-detection)
- [Reading `get_runtime_info`](#reading-get_runtime_info)

## How to read this page

A **tool** is one action the AI assistant can ask the server to perform, such
as "list my flows" or "install a node". You do not call tools yourself: you
write a normal request in your assistant's chat and the assistant picks the
tool. Each tool has a fixed name (for example `list_flows`); the examples under
each group below show the kind of sentence that leads to it. See the
[glossary](glossary.md) for words such as [flow](glossary.md#flow),
[tab](glossary.md#tab), [node](glossary.md#node) and
[Admin API](glossary.md#admin-api).

44 tools, 3 resources, 2 prompts. Tools are classified by risk:

- **read** — no side effects (it only looks; nothing changes).
- **write** — mutates persisted configuration (it changes what is saved in Node-RED).
- **action** — has a runtime side effect that is not persisted (e.g. `inject_node`).

In the tables below, **HTTP** is the [Admin API](glossary.md#admin-api) call
the tool makes (`local` means it never contacts Node-RED), **Risk** is one of
the three classes above, and **Notes** holds the details worth knowing.

A write that changes the flow configuration takes a snapshot (a [backup](glossary.md#backup)) of it first; the
palette writes and `inject_node` do not. See
[Which writes snapshot the flow configuration](#which-writes-snapshot-the-flow-configuration).

The `read` / `write` split is enforced at tool **registration**, not
inside each handler: the 23 mutating tools are not advertised when
`--read-only` is set, so a model cannot call what it cannot see.
`inject_node` counts as mutating — firing an inject can send a real
command to real hardware.

The 21 tools marked `read` are the only ones registered under
`--read-only`.

## Node-type policy

In short: the server refuses to create or change nodes of certain dangerous
types (by default `exec` and `system`, which can run commands on the host).
The list is the [denylist](glossary.md#denylist).

Every write tool that can carry a node type is subject to the
`MCP_NODE_DENYLIST` policy (default `exec,system`), which ignores case and
surrounding whitespace. The policy is
judged on the effective configuration the write would deploy, and the
document is walked recursively, so a forbidden type nested inside a
subflow definition is refused too. `update_node` is judged on the node
as it is after the patch is merged. The refusal names
`MCP_NODE_DENYLIST` and the offending type. It happens before any backup
snapshot is taken and, for every surface whose effective content is known up
front, before the runtime is contacted. Two surfaces cannot know it up front:
`update_node` and `connect_nodes` are judged on what they would produce, so
their refusal follows a read of the tab and never a deployment or a backup.

`add_node`, `update_node`, `create_flow`, `update_flow`, `import_flow`, and
`set_flows` also check submitted node types against the runtime palette before
deploying. Registration uses exact type names. Built-in structural types
(`tab`, `group`, `junction`, `subflow`) and instances of existing subflows are
accepted. If the palette cannot be read, the write is refused. The client
caches the palette and invalidates it after successful MCP palette changes;
restart the server after changing the palette outside MCP.

Denylist-covered surfaces: `create_flow`, `update_flow`, `add_node`,
`update_node`, `connect_nodes`, `import_flow`, `set_flows`,
`restore_backup`, `create_subflow`, `update_subflow`,
`instantiate_subflow`. The server's own `set_context` helper obeys the
same policy. See [`SECURITY.md`](../SECURITY.md).

## Retry safety

In short: looking is retried automatically, changing is not, so that a change
is never applied twice by accident.

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

### Concurrent edits

The granular tools (`add_node`, `update_node`, `delete_node`,
`connect_nodes`, `disable_flow`, `enable_flow`, `instantiate_subflow`) read the
whole configuration once, with the revision that goes with it, and deploy that
same revision. The tab the edit patches is therefore the tab the deployment
carries, and three things follow:

- a change another editor made to a property the edit does not touch survives
  the edit — the edit is applied on top of the current state, not on top of an
  older copy of it;
- a deployment that lands after the snapshot is refused by the runtime with a
  **409**, and the edit is not replayed on top of it;
- a conflict is a conflict: the tool reports it instead of overwriting the
  other editor's work. Read the flow again before retrying.

## Timeout budgets

In short: every call has a time limit; slow operations such as installing a
node get a longer one.

Each tool attempt runs under a context deadline. The default is 15 seconds.
The tools whose work legitimately outlasts it carry their own budget, so the
deadline the MCP wrapper sets does not cut the operation short:

| Tools | Budget | Why |
|---|---|---|
| `install_node`, `uninstall_node` | 5 minutes | npm runs under the hood and takes minutes |
| `set_flows`, `set_flows_state`, `restore_backup` | 2 minutes | a full deploy, a runtime restart or a restore can stall a busy instance |
| every other tool | 15 seconds | the default |

The server adds a short allowance on top of those budgets, so the budget the
runtime work actually gets is the one above and not the time the call spent
waiting for the write lock.

The budget is the server's, not the client's: many MCP clients give up on a
call after their own timeout, so a five-minute install only runs as long as
the client is willing to wait for it.

A client can cancel a call at any time. The cancellation reaches the runtime:
the in-flight request is aborted. What the runtime does with the operation
after that is up to the runtime — an npm install it has already started keeps
running — so a cancelled operation is never replayed automatically.

Cancelling a call means the client stops waiting; it does not mean the
server gets to report the outcome back to the client that walked away. A
write that is cancelled while it is running reports an indeterminate outcome
if the caller is still listening; a write cancelled while it is still waiting
for the write lock reports that no action was started; a cancelled read
reports a failed attempt.

While a long palette operation holds the write lock, a write that arrives
during it is refused once its own deadline passes. The refusal says that no
action was started, and nothing reaches the runtime.

Two locks guard a write, and both waits are cancelable: the server's own
context-helper lock and the client write lock that serializes the mutating
endpoints. A write that arrives while either one is held gives up when its
context is done — canceled by the caller, or past its own deadline — and it
gives up before it snapshots anything or contacts the runtime. The refusal
names the lock it was waiting for:

- `waiting for context helper lock: context canceled; no action was started`
- `waiting for write lock: context canceled; no action was started`

## Backup recovery limits

In short: the automatic [backups](glossary.md#backup) restore the *shape* of
your flows, but not passwords and other secrets stored by Node-RED. Read this
before deleting anything.

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

The read-write server sends the full notice in its MCP initialization
instructions. Flow-configuration write tools still warn in their descriptions
that backups omit credentials, even if the client does not show the server
instructions. `delete_flow` and `restore_backup` repeat the full notice in
their successful responses.

The palette tools and `inject_node` do not advertise these limits: the palette
changes the installed modules and `inject_node` fires a message, and neither
changes the flow configuration. `set_context` does not advertise them either,
even though provisioning its helper tab is a flow-configuration write: the
tool writes into the helper's context, and only provisioning the helper tab
touches the flows. No additional filesystem or secret access is granted to the
MCP server.

### Which writes snapshot the flow configuration

The snapshot is fail-closed: when it cannot be written, the write does not
proceed.

**Snapshots the current configuration:**

- flow tabs: `create_flow`, `update_flow`, `delete_flow`, `enable_flow`,
  `disable_flow`;
- granular edits: `add_node`, `update_node`, `delete_node`, `connect_nodes`;
- the editor clipboard: `import_flow`;
- full deploy and restore: `set_flows`, `restore_backup`;
- subflow definitions: `create_subflow`, `update_subflow`, `delete_subflow`,
  `instantiate_subflow`;
- the runtime state: `set_flows_state` (it starts or stops the runtime, and
  snapshots the configuration before it does);
- the `set_context` helper, when the call has to provision its tab.

**Takes no snapshot:**

- the palette: `install_node`, `uninstall_node`, `enable_node`,
  `disable_node` — they change the installed modules, not the flow
  configuration;
- `inject_node` — it fires a message; nothing is persisted.

## Flows

A [flow](glossary.md#flow) (a [tab](glossary.md#tab) in the editor) is a set of
connected [nodes](glossary.md#node); a [subflow](glossary.md#subflow) is a
reusable group of nodes. These tools read and edit them.

Example requests:

- "Show me all my flows" uses `list_flows`.
- "Find every node that talks to MQTT" uses `search_flows`.
- "Create a new flow called Garden" uses `create_flow`.
- "Add a debug node to the Garden flow" uses `add_node`.
- "Is this flow valid before I deploy it?" uses `validate_flow`.
- "Export the Garden flow so I can share it" uses `export_flow`.

`get_flow`, `export_flow`, `update_flow`, `delete_flow`, `disable_flow`, and
`enable_flow` require a tab identifier. Pass `flow_id` or the original `id`.
If you pass both, they must match; conflicts are rejected before Node-RED is
contacted. For `disable_flow` and `enable_flow`, the ID must also be a single
path segment: it cannot contain `/`, `\`, or `%`, or be `.` or `..`.
Node tools such as `add_node` still use `flow_id` for the tab, and `get_context`
and `set_context` still use `id` for flow and node scopes.

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | Summary by default, `detail="full"` opt-in |
| `search_flows` | `GET /flows` | read | |
| `get_flow` | `GET /flow/:id` | read | |
| `create_flow` | `POST /flow` | write | |
| `update_flow` | `GET /flows` + `POST /flows` | write | Full rewrite; prefer the granular tools; the written tab is judged in the context of the whole configuration |
| `delete_flow` | `DELETE /flow/:id` | write | |
| `set_flows` | `POST /flows` | write | Full deploy, most destructive; `force=true` skips structural-reference validation only; function-node JavaScript syntax is always checked |
| `add_node` | `GET /flows` + `POST /flows` | write | Confirms when supplied outgoing wires were kept; suggests `connect_nodes` only when there are no outgoing wires |
| `update_node` | `GET /flows` + `POST /flows` | write | Merges properties; rejects broken references and lists any removed output ports that still have wires |
| `delete_node` | `GET /flows` + `POST /flows` | write | Cleans incoming wires, links, groups and scope |
| `connect_nodes` | `GET /flows` + `POST /flows` | write | Refuses a port at or past the source's output count (function and trigger `outputs`, switch rules), a `port` that is not a whole number from 0 to 999, and a wire from a node without outputs (debug, http response, link out, mqtt out), into one without inputs (inject, catch, status, link in) or touching a config node, naming the node and the reason; a wire that already exists is answered as already connected, with no deploy and no backup |
| `validate_flow` | local | read | Dry-run structural and function-node JavaScript syntax checks plus advisory warnings for selected core nodes. `func`, `initialize` and `finalize` are parsed as async function bodies; syntax issues report the node, field and location. A flat array is judged as a whole: unique ids across tabs, subflows and config nodes, a type on every entry, and an owner (tab or subflow) for every node and group. An instance of a subflow not in the payload is not a structural issue. A wire from a node without outputs, into one without inputs or touching a config node is reported as `impossible_wire`, a structural issue, on both the tab document and the flat array shapes |
| `disable_flow` | `GET /flows` + `POST /flows` | write | |
| `enable_flow` | `GET /flows` + `POST /flows` | write | |
| `inject_node` | `POST /inject/:id` | action | Excluded from `--read-only`; optional payload |
| `export_flow` | `GET /flows` | read | Editor clipboard format: a flat array with the tab (`"type":"tab"`) first, then its nodes, and the global config nodes and subflow definitions they use (transitively) |
| `import_flow` | `GET /flows` + `POST /flow` | write | Takes the flat editor clipboard (exactly one `tab` element; the nested `[{...tab, nodes}]` shape is still accepted). Ids are kept unless one already exists on the instance; then every node id of the copy is reminted and its wires and references rewritten. Subflows the instance has are reused; a global config node is reused only when the instance has it as a global config node of the same type, otherwise it is created scoped to the new tab (under a reminted id when its id is taken); a missing subflow is refused (create it first with `create_subflow`). An element with canvas coordinates (`x`/`y`) but no `z` is refused. The node-type policy judges every element of the clipboard before the runtime is contacted, including the members of subflow definitions and the global config nodes that end up reused, since an instance of a reused subflow runs the members the instance has. An `http in` route (method + url, leading `/` added as Node-RED does) of the copy that an enabled `http in` node on an enabled tab already serves is still imported, and the result names the route, that node and its tab: deleting the copy later also unregisters the original route until that tab is redeployed (`disable_flow` + `enable_flow`, or any deploy of that tab) |
| `list_subflows` | `GET /flow/global` | read | |
| `get_subflow` | `GET /flow/global` | read | |
| `create_subflow` | `PUT /flow/global` | write | The definition's topology is validated first |
| `update_subflow` | `PUT /flow/global` | write | The definition's topology is validated first; instances wired to a removed output are listed and the update refused, or trimmed with `prune_wires` |
| `delete_subflow` | `PUT /flow/global` | write | Refuses while the definition is still instantiated |
| `instantiate_subflow` | `GET /flows` + `POST /flows` | write | `wires` may not have more ports than the subflow has outputs |

### Deleting a subflow definition

`delete_subflow` refuses to delete a definition that is still
instantiated. An instance node — a node of type `subflow:<id>`, in a
flow tab or nested inside another subflow definition — is the only
thing that keeps a definition alive, and the runtime resolves that
type at deploy time: remove the definition anyway and the instance
keeps pointing at a missing type, the runtime logs `Waiting for
missing types to be registered` on the next start, and the affected
flows never deploy.

The refusal names every reference that has to be removed first, and
the definition is left untouched. The instances are removed with
`delete_node` (an instance in a flow tab) or by rewriting the enclosing
definition with `update_subflow` (an instance nested in another
subflow); `delete_subflow` never removes them for you.

### What a deployment validates

A deployment installs a whole configuration, so it is checked as one
before anything is sent to the runtime. The check is local: nothing
reaches Node-RED, and no backup is taken, when the configuration is
refused.

The rules are the ones the runtime applies to the same array — with one
exception, called out below — and each of them is a reference that does
not resolve:

- **a wire that names no node in the same tab (or the same subflow)** —
  this is the repository's rule, shared with the per-tab write path,
  and it is stricter than the runtime: the runtime does resolve a wire
  across tabs (it falls back to the global node lookup), so such a wire
  delivers. The editor never creates one, and both write paths refuse
  it, so the configuration an operator installs and the configuration
  they can then edit are judged by the same rule;
- **a node whose `z` names neither a tab nor a subflow definition** —
  the runtime invents a tab for it, and the node runs outside the flow
  that was deployed;
- **two entries with the same id** — the runtime keys every node by its
  id, so the last entry wins and the earlier one disappears without a
  word;
- **an instance of a subflow whose definition is not in the
  configuration** — the runtime installs a node type it cannot resolve,
  logs `Waiting for missing types to be registered` and leaves the
  flows stopped.

Two topologies are deliberately left alone, because the runtime owns
them: **link nodes** (the runtime recomputes the wires of a `link out`
node from its `links` array, and a `link in` may point at a `link out`
in another tab — that is what a link is for) and **subflow instances**
(an instance is an ordinary node of its tab, and its wires are checked
against that tab like any other node's).

`set_flows` applies these rules to the array it is given, and
`restore_backup` applies them to the backup it is about to install. A
backup that no longer validates — one taken from a runtime that carried
a broken reference, or one edited by hand — is refused with the list
of problems; `restore_backup` with `force=true` installs it anyway,
and the result says that it was installed unvalidated. That is the
recovery path, and it has to be asked for explicitly. Force skips
structural-reference checks only: invalid function-node JavaScript is
still refused before a backup or deployment.

`set_flows` has the same structural escape hatch (`force=true`), for the
same reason: the rules are the repository's, and one of them is stricter
than the runtime. The node-type policy and the rule that a deployment
must leave at least one tab are **not** skipped by it — they are not
about the references of the configuration but about what a deployment
may do to the runtime. Function syntax validation remains mandatory with
or without force.

`validate_flow` and deployment checks reject dynamic `import()` inside a
Function node. Node-RED 5.0.10 parses it, but its Function node sandbox does
not provide the callback needed to load a module that way. To use a module,
add it in the Function node's **Setup** tab and use the configured variable
in your code. This requires `functionExternalModules` to be enabled in
Node-RED.

That tab rule is decided by decoding the `type` of every entry, not by
searching the payload for text. The same document behaves the same
whether it arrives as a compact string, as an indented string or as an
array of objects, and a `type: "tab"` mentioned inside a property — a
nested object, or a piece of text — is not a tab.

`update_flow` writes a single tab, and it is judged in the context of
the whole configuration: the entries of the tab it writes are the
caller's, and the rules that need the whole configuration (an instance
of a subflow whose definition is not there) are applied to them. The
rest of the configuration is the runtime's own state, and a problem
there is not the caller's to answer for — refusing it would make the
runtime impossible to repair.

`create_subflow` and `update_subflow` share the guard the tab write
path has always applied: a wire inside the definition that names a
node which is not in it, and a node whose `z` names another tab, are
refused before the runtime is contacted. A subflow definition is also
judged by its ports: the runtime resolves the ports of a subflow
against the definition's own members, so an `in` port that names a
node which is not there makes the runtime throw when it starts an
instance of that subflow, and an `out` port that names one logs
`Unknown node referenced inside subflow` and the output never fires.
The one legal reference that is not a member is the definition's own
id on an `out` port — that is how a subflow wires its input straight
to its output.
Every wire of an `out` port must also carry a `port` that is a
non-negative integer (`{"id":"fn","port":0}`): the runtime indexes the
target's wires by it, so a missing or invalid `port`, or one past the
outputs of the internal node (as many as its `wires` array has), never
fires, and on a pass-through to the definition's own id a `port` that
is not below the number of `in` ports makes the start throw. These are
reported as `invalid_subflow_port` by `create_subflow`, `update_subflow`,
`set_flows`, `restore_backup` and `validate_flow`. The wires of an `in`
port carry only an `id`.

The number of `out` ports also bounds every instance of the subflow, on
whichever path writes one: `add_node`, `update_node` (on the node as the
deployment would carry it), `connect_nodes` (the source instance) and
`instantiate_subflow` all refuse `wires` with more ports than the definition
has outputs, and none of them deploys on a refusal. A `wires` value that is
not an array of arrays is refused on all four. `update_subflow` refuses a
definition that would leave an instance wired to an output that no longer
exists, listing the instances and writing nothing, unless `prune_wires` is
set: then those wires are removed first, tab by tab, and inside any other
definition that nests an instance.

### Semantic warnings in `validate_flow`

`ok` and `issues` still describe structural validation. The optional
`warnings` array is advisory and never affects writes. It reports:

- `output_count_mismatch`: the number of groups in `wires` differs from
  the declared outputs of a Function or Trigger node, or from the number
  of Switch rules when available. Empty groups count as output slots.
- `invalid_property_type`: a Change rule's `pt` (or a Move rule's
  destination `tot`) is not `msg`, `flow` or `global`, or a Switch's
  `propertyType` is not one of those types, `jsonata` or `env`.
  This does not check comparison or literal value types.
- `http_response_unreachable`: no HTTP Response is reachable from an
  HTTP In by following wires and static links in the supplied document.
  A path through a subflow, Link Call or link to a missing target is
  inconclusive and suppresses the warning. This does not guarantee a
  response at runtime or check for multiple reachable responses.

The checks are local; they do not query the runtime or inspect contributed
node definitions.

## Palette

The [palette](glossary.md#palette) is the list of node types available in the
editor. These tools look at it, search the public npm registry for more, and
install, remove, enable or disable them.

Example requests:

- "Which nodes are installed?" uses `list_nodes`.
- "Is there a node for Telegram?" uses `search_nodes`.
- "Install node-red-contrib-influxdb" uses `install_node`.
- "Disable that module for now" uses `disable_node`.

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `list_nodes` | `GET /nodes` | read | |
| `get_node_info` | `GET /nodes/:module` | read | |
| `search_nodes` | npm registry | read | Public registry (`registry.npmjs.org`) |
| `install_node` | `POST /nodes` | write | |
| `uninstall_node` | `DELETE /nodes/:module` | write | |
| `enable_node` | `PUT /nodes/:module[/:set]` | write | |
| `disable_node` | `PUT /nodes/:module[/:set]` | write | |

If `install_node` returns HTTP 404, use `search_nodes` to find the correct npm
package name before retrying.

When Node-RED returns its generic `unexpected_error` / `Install failed` response
for either palette operation, the error points operators to Node-RED's runtime
output (stdout, `journalctl`, `docker logs`, or a configured `settings.js`
logging handler), where npm's details are written. Common causes include failing
lifecycle scripts and an incompatible Node version. A manual user-directory
install with `npm install --ignore-scripts <module>` may be a workaround; verify
the package is trusted first. Skipping lifecycle scripts can make some packages
unusable.

## Runtime, diagnostics, recovery

These tools look at how Node-RED itself is doing (settings, logs, debug
output, [context](glossary.md#context) values), start or stop the flows, and
list or restore [backups](glossary.md#backup).

Example requests:

- "What version of Node-RED is this, and is it healthy?" uses `get_diagnostics` and `get_runtime_info`.
- "What did the debug nodes print in the last minutes?" uses `get_debug_messages`.
- "Show me the Node-RED logs" uses `get_runtime_logs`.
- "What is stored in the global variable `counter`?" uses `get_context`.
- "Show me my backups" uses `list_backups`.
- "What changed between that backup and now?" uses `diff_flows`.
- "Go back to the backup from this morning" uses `restore_backup`.

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | |
| `get_diagnostics` | `GET /diagnostics` | read | Requires Node-RED ≥3.1 |
| `get_flows_state` | `GET /flows/state` | read | Requires Node-RED ≥3.0.0; not gated by `runtimeState` |
| `get_context` | `GET /context/...` | read | editor-api, no stability contract |
| `set_context` | `GET /flows`, `POST /flow`, `POST /inject/:id` | write | Verified persistent inject/function helper |
| `get_debug_messages` | `/comms` WebSocket | read | Buffer of 500, reconnects |
| `get_runtime_logs` | journal / stream | read | |
| `list_plugins` | `GET /plugins` | read | editor-api |
| `get_runtime_info` | companion to `get_diagnostics` | read | MCP server view of the runtime |
| `get_node_status` | `/comms` WebSocket | read | |
| `set_flows_state` | `POST /flows/state` | write | Requires `settings.runtimeState.enabled: true` |
| `list_backups` | local | read | |
| `diff_flows` | local + `GET /flows` | read | |
| `restore_backup` | `POST /flows` | write | The backup is structurally validated first; `force=true` skips structural checks only, not function syntax validation |

## Context helper recovery

`set_context` discovers its helper in `GET /flows` before every call, including
calls after restarting the MCP process. A supported helper has one isolated
inject/function pair with the expected function code, configuration and wiring;
the tab label alone is not proof of identity. A valid helper is reused without
a configuration deployment. The configured node denylist also applies to reuse.

When no valid helper exists, the server creates a complete tab in one guarded
`POST /flow`, with node IDs that do not collide with existing configuration.
There are no intermediate empty-tab deployments. A failed or ambiguous response
is returned without automatic replay; a later explicit call first discovers any
helper that the runtime actually installed. Existing edited, partial or unrelated
tabs are left untouched rather than deleted or overwritten.

Deleting or replacing the helper through `delete_flow`, `set_flows`, a backup
restore or the editor does not permanently invalidate the MCP server: the next
call discovers a valid helper or provisions a new one. Recovery does not migrate
old helper flow/node context; use `list_flows` to discover current IDs for those
scopes. Global context remains owned by Node-RED, not by the helper tab.

Writes from this MCP server are serialized with helper verification and inject
dispatch. Independent editor sessions, other MCP processes and external Admin API
clients do not share that lock: avoid concurrent helper edits/provisioning from
those clients. This is not a distributed transaction or a cross-process singleton.

## Resources (3)

A [resource](glossary.md#resource) is a piece of read-only data the assistant
can open directly, without calling a tool.

| URI | Description |
|---|---|
| `nodered://flows/current` | The full current flow configuration |
| `nodered://settings` | Server settings |
| `nodered://flows/state` | Runtime state |

## Prompts (2)

A [prompt](glossary.md#prompt) is a ready-made request template the client can
offer you, for example "explain this flow".

| Name | Description |
|---|---|
| `explain_flow` | Describe what a flow does, its triggers, and external dependencies |
| `generate_flow` | Build a flow from a plain-English description |

## Node-RED version detection

A few tools need a minimum Node-RED version: `set_context` and
`inject_node` with a payload need 5.0.0, `get_diagnostics` needs
3.1.0, and `get_flows_state` needs 3.0.0. The MCP reads the version from
`GET /settings` at startup and reports these requirements in
`get_runtime_info`. For `set_context` and `inject_node` with a payload,
the MCP also checks the version before the operation, so that check
does not depend on an earlier call having warmed the cache.

Two properties are worth knowing:

- **A local runtime is detected like any other.** The address of the
  runtime (`http://localhost:1880` is the default) does not change
  the check.
- **A failed probe is not final.** If the runtime is not reachable
  when the MCP starts, the version stays unknown until the next
  attempt, and a later gated operation probes again.

When the version cannot be detected, `get_runtime_info` reports the
capability as `unknown`. If Node-RED denies access to `/settings` with `401`
or `403`, the tool instead returns an error that names the credentials to
configure (see [Troubleshooting](troubleshooting.md)). Calls that check the
version are **not** refused
on that basis alone. Their success message carries the suffix "Node-RED
version was not detected; this call was not verified against the minimum
version".

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
      "set_flows_state": "setting_disabled"
    },
    "capabilityGuidance": {
      "set_context": {
        "reason": "Node-RED 3.0.0 is below the 5.0.0 minimum required by set_context",
        "remedy": "Upgrade Node-RED to at least 5.0.0"
      },
      "set_flows_state": {
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
known fix to recommend. A `/comms` tool that has not been observed yet
reports `stream_unknown` instead, so each state keeps one meaning.

Every reason is derived from a probe the handler already ran. Nothing
here is inferred, and no mutating endpoint is touched to produce it. The
version reason names the actual minimum for that specific tool, so
`set_context` (needs 5.0.0) and `get_diagnostics` (needs 3.1.0) give
different advice.

Some states have more than one possible cause and the probes cannot tell
them apart, so the reason states the observation rather than a verdict.
`setting_disabled` covers an operator-closed gate, a missing setting
key, and an unreadable `/settings` alike.

The two tools that read the `/comms` WebSocket — `get_node_status` and
`get_debug_messages` — are classified from **two** things: the
`MCP_DEBUG_STREAM` opt-in (configuration) and what the MCP observed of
the WebSocket (availability). An enabled flag on its own is never
reported as `ok`:

- `stream_disabled` — the opt-in is off: `MCP_DEBUG_STREAM` was not set.
- `ok` — the tail's last observed connection was successful.
- `stream_disconnected` — the tail dialled and failed, or a connection
  it had was lost; the reason carries the last error.
- `stream_unavailable` — no tail exists: the `/comms` endpoint could
  not be derived from the configured Node-RED URL.
- `stream_unknown` — the tail has not reported a connection yet: it is
  still dialling, or the dial is hanging against an unreachable host.

Each of the two tools is classified from its own tail, so one can be
`ok` while the other is `stream_disconnected`.

`ok` is the last thing the MCP observed, not a liveness check: a
network partition can leave a half-open socket that still looks
connected.
