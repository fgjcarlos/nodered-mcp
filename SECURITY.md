# Security

This document covers the security model for `nodered-mcp` and the
operator's responsibilities when wiring it up. It is the primary
reference for the RCE-defence knobs (`MCP_NODE_DENYLIST`,
`MCP_READ_ONLY`, `MCP_HTTP_TOKEN`) — see the linked issues and the
README for context.

## Threat model

`nodered-mcp` is an MCP server that talks to a Node-RED instance over
its admin HTTP API. Every caller of an MCP write tool
(`create_flow`, `update_flow`, `add_node`, `set_flows`,
`import_flow`, `update_node`) is effectively a deployer for that
instance. Any node type the Node-RED runtime has installed — including
shell-executing ones — can be deployed through these tools.

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

The check runs in the MCP layer, BEFORE the runtime is contacted and
BEFORE a backup snapshot is taken. A denied node type returns a typed
error to the model and never touches the live instance:

```
node type "exec" is in MCP_NODE_DENYLIST; remove it from the denylist
or use a different node type (see SECURITY.md)
```

The check is **case-sensitive** because Node-RED node types are. It
is per-node — a single bad node rejects the entire write (so a model
that builds a flow mixing safe and dangerous types gets the same
defence as one that ships a payload of pure `exec`).

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
