# Security

This document covers the security model for `nodered-mcp` and the
operator's responsibilities when wiring it up. It is the primary
reference for the RCE-defence knobs (`MCP_NODE_DENYLIST`,
`MCP_READ_ONLY`, `MCP_HTTP_TOKEN`) — see the linked issues and the
README for context.

## Threat model

`nodered-mcp` is an MCP server that talks to a Node-RED instance over
its admin HTTP API. Every caller of an MCP write tool
(`create_flow`, `update_flow`, `add_node`, `update_node`, `import_flow`,
`set_flows`, `create_subflow`, `update_subflow`, `restore_backup`) is
effectively a deployer for that instance. Any node type the Node-RED
runtime has installed — including shell-executing ones — can be deployed
through these tools.

The most acute risk is **remote code execution on the Node-RED host**
via the Node-RED `exec` and `system` node types:

- `exec` runs a configured shell command whenever the node fires.
- `system` runs shell commands directly from msg fields at runtime.

Both execute with the same privileges as the Node-RED process. A
caller who can reach an MCP write tool can therefore run any shell
command on the Node-RED host simply by deploying a flow that
references one of these node types.

Issue #81 documents the unfiltered-payload risk in detail.

## Default posture: defense in depth

Out of the box, `nodered-mcp` defaults to a deny-list that blocks the
`exec` and `system` node types. The relevant knob is
`MCP_NODE_DENYLIST`:

| Environment         | Behaviour                                           |
| ------------------- | --------------------------------------------------- |
| Unset (default)     | `MCP_NODE_DENYLIST=exec,system` (RCE-resistant)    |
| `MCP_NODE_DENYLIST=` (empty) | Operator opt-out — every node type is allowed. |
| `MCP_NODE_DENYLIST=exec,system,foo,bar` | Custom denylist (replaces defaults). |

The check runs in the Node-RED client, BEFORE the runtime is contacted and
BEFORE a backup snapshot is taken. It is evaluated on the **effective
configuration the write would deploy** — the document as it will be sent,
not the request as it arrived — and it walks that document recursively,
so a forbidden node type nested inside a subflow definition is found too.
A denied node type returns a typed error to the model and never touches
the live instance:

```
node type "exec" is in MCP_NODE_DENYLIST; remove it from the denylist
or use a different node type (see SECURITY.md)
```

It covers every write surface that can carry a node type:
`create_flow`, `update_flow`, `add_node`, `update_node`, `connect_nodes`,
`import_flow`, `set_flows`, `restore_backup`, `create_subflow`,
`update_subflow` and `instantiate_subflow`. The server's own
`set_context` helper is subject to the same policy: a denylist naming
`function` or `inject` stops the helper from being provisioned.

The check is **case-sensitive** because Node-RED node types are. It
is per-node — a single bad node rejects the entire write (so a model
that builds a flow mixing safe and dangerous types gets the same
defence as one that ships a payload of pure `exec`).

### What is judged

The policy is evaluated on the content the write would deploy, walked
recursively over the admin API's own shapes (`nodes`, `configs`,
`subflows`), so a forbidden type nested inside a subflow definition is
found too.

- A write that submits a whole document — `create_flow`, `update_flow`,
  `import_flow`, `set_flows`, `restore_backup`, `create_subflow`,
  `update_subflow` — is judged in full, recursively.
- A write that edits one part of a tab is judged on the part it
  introduces or touches: the node `add_node` appends, the node
  `update_node` patches (**after** the patch is merged, so a patch that
  changes a node's `type` is refused on the type it would end up with),
  and the two nodes `connect_nodes` wires together.
- A forbidden type that already exists in the runtime is not judged by a
  write that leaves it alone: the policy governs what a call introduces
  and what it arms, not what the operator already deployed by hand. A
  write that *touches* such a node — patching it, or wiring it — is
  refused, because that is how a forbidden node gets armed.

### When the refusal happens

Before the runtime is contacted and before any backup snapshot is
taken, for every surface where the effective content is known up front.

Two checks cannot know that before reading the runtime: the node
`update_node` would produce, and the nodes `connect_nodes` would wire.
Those refusals follow a read of the tab, and never a deployment and
never a backup.

## How to harden an install

1. **Default denylist.** Keep `MCP_NODE_DENYLIST` unset (the
   default) unless you have a specific reason to allow shell-executing
   node types. The default blocks `exec` and `system`.

2. **Read-only mode for inspection-only clients.** Start the server
   with `MCP_READ_ONLY=true` (or `--read-only`) for any client that
   only needs to observe the instance. Every write tool is
   withheld from the advertised surface, so a model cannot call what
   it cannot see.

3. **HTTP transport requires authentication.** If you bind the HTTP
   transport to anything other than loopback, the server refuses to
   start without `MCP_HTTP_TOKEN` (or `MCP_OAUTH_ISSUER` + audience).
   Without this guarantee the write tools would be reachable to
   anyone who can reach the port.

4. **Restrict Node-RED network access.** The MCP server is a thin
   client of Node-RED's admin API. Anyone who can call the MCP
   write tools can deploy flows; anyone who can deploy flows can
   also exercise whatever node types `MCP_NODE_DENYLIST` allows. Bind
   Node-RED's admin port to a network the MCP client is allowed to
   reach, and nothing else.

5. **Audit backups.** Every write takes a backup of the current flow
   config first. The default directory is `./backups`; configure
   `NODERED_BACKUP_DIR` to a durable path you can audit after an
   incident.

## Operator opt-out

If you intentionally need `exec` or `system` (e.g. for shell-driven
automation that the denylist would otherwise block), set
`MCP_NODE_DENYLIST` to an empty string to disable the check:

```sh
MCP_NODE_DENYLIST="" nodered-mcp
```

With this setting, the write tools forward every node type without
filtering. **This is an explicit acknowledgement that you accept the
RCE risk** — any caller of the write tools can now deploy shell-
executing nodes. Pair the opt-out with the rest of this document's
hardening steps (read-only mode, restricted network access, auth on
the HTTP transport).

To allow only specific node types, pass a comma-separated list:

```sh
MCP_NODE_DENYLIST="exec,system,my-custom-shell-node" nodered-mcp
```

The list replaces the default — it does not extend it. Pass every
type you want blocked, including `exec` and `system`, in one value.

## Rate limiting behind a reverse proxy

The HTTP transport throttles per source IP, resolved from the socket's
`RemoteAddr` only. No request header is consulted.

**Behind a reverse proxy, every client shares one bucket.** If
`nodered-mcp` binds to loopback and a reverse proxy fronts it, the
socket address it sees is the proxy's. All clients then contend for a
single `MCP_HTTP_RATE_PER_SEC` allowance, so:

- one noisy client can throttle everyone else, and
- the limiter stops being a per-client control entirely.

This is a deployment limitation, not an authentication bypass —
`MCP_HTTP_TOKEN` / OAuth still gate every request. The rate limiter is
defence in depth, not the authentication boundary.

**Enforce per-client limits at the proxy.** That is the layer that
actually knows the real client. Keep the MCP token or OAuth
requirement in place; the two controls are complementary.

**Do not trust `X-Forwarded-For` blindly.** Honouring it without a
trusted-proxy policy is trivially spoofable — any client can send the
header itself and evade per-IP throttling, or aim it at someone else
to get them throttled. Supporting forwarded addresses would need an
explicit list of trusted proxy hops, and spoofing tests alongside it.
Until that exists, the header is ignored on purpose.

## Reporting a vulnerability

Open a private security advisory on GitHub (or contact the
maintainers directly through the channels listed in `README.md`).
Please do not open a public issue for suspected vulnerabilities.

### Supported versions

Security fixes are backported to the most recent **minor** release
line (for example, if the latest release is `v0.7.x`, fixes land
there and are also backported to `v0.6.x` if it is still receiving
maintenance). Earlier versions are not patched; please upgrade.

### Response expectations

- **Initial acknowledgement**: within 5 business days of the report.
- **Triage outcome** (accepted / declined / needs-more-info): within
  10 business days of the initial acknowledgement.
- **Fix timeline**: agreed with the reporter after triage. Critical
  RCE-class issues are prioritised.

Coordinated disclosure is appreciated: please give the maintainers
a reasonable window (typically up to 90 days) before publishing
exploitable details so users have time to upgrade.
