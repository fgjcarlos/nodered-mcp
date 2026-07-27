# nodered-mcp

An [MCP (Model Context Protocol)](https://modelcontextprotocol.io) server, written in Go, that exposes the Node-RED admin API to AI clients as tools, resources, and prompts.

```
MCP client  ──stdio | HTTP──▶  nodered-mcp  ──HTTP──▶  Node-RED :1880
```

`nodered-mcp` is provider-agnostic. The same binary works with any MCP-capable client — Claude Desktop, Claude Code, Cursor, VS Code, Gemini CLI, Cline — regardless of the underlying model.

A Spanish version of this document is available at [`README.es.md`](./README.es.md).

## Contents

- [Capabilities](#capabilities)
- [Safety model](#safety-model)
- [Requirements](#requirements)
- [Installation](#installation)
- [Configuration](#configuration)
- [Command line](#command-line)
- [Transports](#transports)
- [Client integration](#client-integration)
- [Troubleshooting](#troubleshooting)
- [Architecture](#architecture)
- [Development](#development)
- [Roadmap](#roadmap)

## Capabilities

29 tools, 3 resources, and 2 prompts. Tools are classified by risk: **read** is side-effect free, **write** mutates persisted configuration and takes a backup first, **action** has a runtime side effect that is not persisted.

### Flows

| Tool | Endpoint | Risk | Description |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | Map of tabs, subflows, node counts, and node types |
| `search_flows` | `GET /flows` | read | Find nodes anywhere by free text or node type |
| `get_flow` | `GET /flow/:id` | read | A single flow tab by ID |
| `create_flow` | `POST /flow` | write | Create a new flow tab |
| `update_flow` | `PUT /flow/:id` | write | Replace an existing flow tab |
| `delete_flow` | `DELETE /flow/:id` | write | Delete a flow tab and its nodes |
| `set_flows` | `POST /flows` | write | Full deployment — replaces the entire configuration |
| `add_node` | `PUT /flow/:id` | write | Add one node without touching the rest |
| `update_node` | `PUT /flow/:id` | write | Change one node's properties, merging not replacing |
| `delete_node` | `PUT /flow/:id` | write | Remove one node and the wires pointing at it |
| `connect_nodes` | `PUT /flow/:id` | write | Wire one node's output to another |
| `inject_node` | `POST /inject/:id` | action | Fire an inject node on demand |

### Palette

| Tool | Endpoint | Risk | Description |
|---|---|---|---|
| `list_nodes` | `GET /nodes` | read | Installed node modules, versions, and enabled state |
| `get_node_info` | `GET /nodes/:module` | read | Metadata for one installed module |
| `search_nodes` | npm registry | read | Search the public catalogue before installing |
| `install_node` | `POST /nodes` | write | Install a module from npm |
| `uninstall_node` | `DELETE /nodes/:module` | write | Remove an installed module |
| `enable_node` | `PUT /nodes/:module[/:set]` | write | Enable a module or one of its node sets |
| `disable_node` | `PUT /nodes/:module[/:set]` | write | Disable without uninstalling |

### Runtime and recovery

| Tool | Endpoint | Risk | Description |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | Server configuration: auth scheme, port, theme, plugins |
| `get_diagnostics` | `GET /diagnostics` | read | Node.js version and memory, OS, container detection |
| `get_flows_state` | `GET /flows/state` | read | Whether the runtime is started or stopped |
| `get_context` | `GET /context/...` | read | State the flows keep between messages |
| `get_debug_messages` | `/comms` WebSocket | read | Output the flows actually produced |
| `list_plugins` | `GET /plugins` | read | Editor plugins loaded by the runtime |
| `set_flows_state` | `POST /flows/state` | write | Start or stop the runtime |
| `list_backups` | local | read | Saved flow snapshots, newest first |
| `diff_flows` | local + `GET /flows` | read | What changed between a snapshot and now |
| `restore_backup` | `POST /flows` | write | Roll the entire configuration back to a snapshot |

### Working with large instances

The full flow configuration of a real instance is far too large to hand to a model verbatim. A 150-node setup is roughly 30,000 characters; a few hundred nodes will exhaust the context window before any work begins.

Two tools exist to avoid that.

`list_flows` returns a compact map by default — tabs, subflows, node counts, and a per-type breakdown, with no node bodies. On that same 150-node instance the summary is about 1,600 characters against 30,000 for the full document. Pass `detail="full"` when you genuinely need everything.

`search_flows` finds nodes without downloading the configuration. The text query is matched case-insensitively against each node's complete JSON, so it locates values living in node-specific fields — an MQTT topic, an HTTP url, a node name, a line inside a function body — without this server needing to know any node type. Each hit reports the node verbatim plus the tab that owns it, which is what a subsequent `get_flow` or `update_flow` needs.

```
search_flows(query: "sensors/room13/temperature")   -> 4 nodes, across 4 tabs
search_flows(type: "function", limit: 3)            -> 16 matched, first 3 shown
```

When results are truncated the response states the true total, so a capped list is never mistaken for a complete one.

### Diagnosing behaviour, not just structure

Reading the flows tells you what an instance is *supposed* to do. Three tools cover why it might not be doing it.

`get_diagnostics` answers "what is this actually running on" in one call: Node.js version and memory, operating system, whether it sits in a container, locale and timezone. Requires Node-RED 3.1 or later; on older versions the tool says so rather than returning a bare 404.

`get_context` reads the state flows keep between messages. Context appears nowhere in the flow JSON, so a flow can look entirely correct and still misbehave because of a value it stored earlier — this is the only way to see that from outside the editor. Scope `global` is instance-wide; `flow` and `node` take the tab or node id. Omit `key` for the whole store.

```
get_context(scope: "global")                      -> {"memory":{"temperature":{"msg":"21.5","format":"number"}}}
get_context(scope: "global", key: "temperature")  -> {"msg":"21.5","format":"number"}
get_context(scope: "flow", id: "tabA")            -> {"memory":{"counter":{"msg":"7","format":"number"}}}
```

Context is read-only here because the admin API exposes no way to write it — there is no hidden setter this server declines to expose.

`list_plugins` lists editor plugins, which extend the editor rather than adding nodes and therefore never appear in `list_nodes`.

### Editing one node instead of a whole tab

`update_flow` replaces an entire tab, which means a model has to reproduce every node exactly. Anything it fails to reproduce is destroyed — and Node-RED nodes carry type-specific fields no schema knows about.

The granular tools avoid that. Each reads the tab, changes only what was asked for, and writes it back through the same guardrails: wires validated, backup taken.

```
add_node(flow_id, node)                      -> appends, leaves everything else byte-identical
update_node(flow_id, node_id, properties)    -> merges the keys given, keeps the rest
delete_node(flow_id, node_id)                -> removes it and cleans up incoming wires
connect_nodes(flow_id, from_id, to_id, port) -> appends to that output port
```

`update_node` merges rather than replaces. Retuning an MQTT topic leaves the broker reference, QoS, and position untouched. Deleting a node also strips wires aimed at it, because Node-RED accepts wires pointing at nothing and simply never delivers to them — a flow that looks intact and quietly does less than it should. `connect_nodes` appends to the port you name and grows the wires array when that port does not exist yet, instead of rewriting the array by hand.

Guarded: a duplicate node id is rejected, a node's id cannot be changed (the wires reference it), and wiring to a node that does not exist is refused.

One detail worth knowing when reading a tab: Node-RED splits its contents into `nodes` and `configs`, deciding by whether the object carries `x`/`y` canvas coordinates. A shared MQTT broker belongs to the tab but appears under `configs`. These tools honour that split, so config nodes can be edited too and never end up filed in the wrong place.

`diff_flows` compares any two configurations — a backup against the live instance, or two backups — and reports what was added, removed, or changed. Since a backup is taken before every write, `diff_flows(from: "latest")` answers "what did that last change actually do".

### Closing the loop: seeing what a flow did

Every other tool here describes the instance. `get_debug_messages` reports what it actually produced — the output of debug nodes, exactly as the editor's debug sidebar shows it.

That completes the cycle a model needs to work unattended:

```
create_flow / update_flow  ->  inject_node  ->  get_debug_messages  ->  fix and repeat
```

Without the last step a model can deploy a flow and never learn whether it worked.

Node-RED publishes this only over the editor's `/comms` WebSocket; there is no HTTP endpoint for it. `nodered-mcp` therefore opens that connection at startup and keeps a rolling buffer of the most recent 500 messages, so output produced *before* you thought to ask is already captured. Pass `since` (an RFC 3339 timestamp) to see only what arrived after a given moment — typically just before you injected.

The connection is maintained in the background and is deliberately never fatal:

- If Node-RED is unreachable, the server still starts and every other tool works.
- A redeploy or restart bounces the runtime; the tail reconnects with capped exponential backoff.
- An empty result distinguishes "not connected", "connected but nothing has arrived", and "nothing matched your filter" — silence is never ambiguous.
- If the buffer overflows, the response says how many older messages were discarded.

When `adminAuth` is enabled the same token authenticates the WebSocket. It needs the `status.read` permission; without it Node-RED rejects the handshake and the reason is reported in the tool's output.

### Resources

| URI | Description |
|---|---|
| `nodered://flows/current` | The full current flow configuration |
| `nodered://settings` | Server settings |
| `nodered://flows/state` | Runtime state |

### Prompts

| Name | Description |
|---|---|
| `explain_flow` | Describe what a flow does, its triggers, and its external dependencies |
| `generate_flow` | Build a flow from a natural-language description |

## Safety model

Handing an LLM write access to a running automation runtime demands guardrails. Three are built in.

**Flow documents are treated as opaque JSON.** Node-RED's node model is deliberately schemaless: an MQTT node carries `topic` and `broker`, a function node carries `func`, an inject node carries `payload` and `repeat`. Modelling that with fixed Go structs would silently drop every unrecognised field on a read/write round-trip. `nodered-mcp` passes flow JSON through verbatim and parses only the specific fields it needs, where it needs them. No field is ever lost.

**Every mutating operation takes a backup first, and fails closed.** Before any write, the server fetches the complete flow configuration and writes it to a timestamped file under `NODERED_BACKUP_DIR`. If the snapshot cannot be written — Node-RED unreachable, directory not writable — the write is aborted rather than risking an unrecoverable change. `list_backups` and `restore_backup` expose the rollback path.

**Wires are validated before the write reaches the runtime.** Node-RED accepts dangling wire targets silently, leaving broken connections behind. `create_flow` and `update_flow` reject any document whose wires point at a node that does not exist within it, which catches the most common failure mode of LLM-generated flow JSON.

Backup filenames are restricted to bare names, so `restore_backup` cannot be used to read arbitrary files from disk.

### Read-only mode

For a production instance, the safest guardrail is not to offer the dangerous tools at all.

```bash
nodered-mcp serve --read-only        # or MCP_READ_ONLY=true
```

The server then registers only the 14 side-effect-free tools. The 15 mutating ones are never advertised, so a model cannot call what it cannot see — this is enforced at registration, not by a check inside each handler.

`inject_node` is treated as mutating and withheld. It writes no configuration, but firing an inject can send a real command to real hardware.

Resources and prompts remain available: all three resources are read-only views, and prompts are inert text.

| Mode | Tools | Resources | Prompts |
|---|---|---|---|
| default | 29 | 3 | 2 |
| `--read-only` | 14 | 3 | 2 |

## Requirements

- A reachable Node-RED instance (1.x or later) with its admin API enabled.
- Nothing else at runtime: `nodered-mcp` is a single static binary.
- Go 1.25+ only if you build from source.

Runs on Linux, macOS, and Windows (amd64 and arm64).

## Installation

Pick one method, then [connect your client](#client-integration).

> **Availability.** Options B, C, and D work today. Option A needs a tagged release: the repository is public, but no `vX.Y.Z` tag has been pushed yet, so the Releases page is empty — see [`issues/301`](./issues/301-publish-repo.md).

### Option A — Prebuilt binary

Download the archive for your platform from [Releases](https://github.com/fgjcarlos/nodered-mcp/releases) and place the binary on your `PATH`.

```bash
# adjust the filename to your OS and architecture
curl -sSL https://github.com/fgjcarlos/nodered-mcp/releases/latest/download/nodered-mcp_Linux_x86_64.tar.gz | tar xz
sudo mv nodered-mcp /usr/local/bin/
nodered-mcp version
```

On Windows, download the `.zip`, extract it, and move `nodered-mcp.exe` into a directory on the `PATH`.

### Option B — go install

```bash
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
```

The binary lands in `$(go env GOPATH)/bin`; make sure that directory is on your `PATH`.

`@latest` resolves to the newest tag, or to a pseudo-version of the default branch while no tag exists. Either way `nodered-mcp version` reports what was actually installed: `go install` applies no linker flags, so the version is recovered from the module information the toolchain embeds.

### Option C — Docker

```bash
docker build -t nodered-mcp .
docker run --rm -p 8090:8090 \
  -e NODERED_URL=http://host.docker.internal:1880 \
  -e NODERED_TOKEN=your-token \
  nodered-mcp
```

The MCP endpoint is then `http://localhost:8090/mcp`. The image defaults to the HTTP transport, since stdio is meaningless inside a container. On Linux, if Node-RED runs on the host, add `--add-host=host.docker.internal:host-gateway`.

### Option D — From source

```bash
git clone https://github.com/fgjcarlos/nodered-mcp
cd nodered-mcp
go build -o nodered-mcp ./cmd/nodered-mcp
```

## Configuration

Every setting can be supplied as an environment variable or a command-line flag. Precedence is flag, then environment variable, then default.

| Variable | Flag | Default | Description |
|---|---|---|---|
| `NODERED_URL` | `--url` | `http://localhost:1880` | Node-RED base URL |
| `NODERED_TOKEN` | `--token` | — | Bearer token, when admin auth is enabled |
| `NODERED_USERNAME` | — | — | Basic auth username, as an alternative to the token |
| `NODERED_PASSWORD` | — | — | Basic auth password |
| `NODERED_INSECURE` | — | `false` | Skip TLS verification. Development only |
| `NODERED_BACKUP_DIR` | — | `backups` | Where flow snapshots are written before each write |
| `MCP_READ_ONLY` | `--read-only` | `false` | Expose only tools that cannot modify Node-RED |
| `MCP_TRANSPORT` | `--transport` | `stdio` | `stdio` or `http` |
| `MCP_HTTP_ADDR` | `--http-addr` | `:8090` | Listen address for the HTTP transport |
| `MCP_HTTP_TOKEN` | `--http-token` | — | Bearer token for the HTTP transport. Required unless bound to loopback |
| `MCP_LOG_LEVEL` | `--log-level` | `info` | `debug`, `info`, `warn`, or `error` |

For local development, a `.env` file in the working directory is loaded automatically. Environment variables already set always win.

```bash
cp .env.example .env
```

## Command line

```
nodered-mcp                    start the server (equivalent to `nodered-mcp serve`)
nodered-mcp serve --read-only  start with the mutating tools withheld
nodered-mcp serve --help       list every flag
nodered-mcp init               generate a configuration snippet for your MCP client
nodered-mcp version            print the version
```

### The init command

`init` detects which MCP clients are installed, prompts for the Node-RED URL, token, and backup directory, and **resolves the absolute path of the running binary**. The generated snippet therefore never points at a non-existent executable, which is the usual cause of a "Server disconnected" error.

| Invocation | Behaviour |
|---|---|
| `nodered-mcp init` | Print the snippet for you to paste |
| `nodered-mcp init --write` | Write directly into the client configuration |
| `nodered-mcp init --all` | Show every known client, not only the detected ones |

`--write` performs a safe merge that preserves any other servers already configured, and saves a `.bak` of the previous file. It is supported for Claude Desktop, Cursor, and Gemini CLI. For VS Code, whose configuration is workspace-scoped, and for Claude Code, which is configured through its own CLI, `init` prints the instruction instead.

## Transports

**stdio** (default). The MCP client launches the binary and communicates over stdin/stdout. This is what Claude Desktop, Claude Code, Cursor, VS Code, and Gemini CLI use.

**http** (streamable HTTP). A single long-running process serves several clients. The MCP endpoint is at `<addr>/mcp`.

```bash
nodered-mcp serve --transport http --http-addr :8090
```

### Authenticating the HTTP transport

The HTTP transport exposes every tool — deploying flows, installing modules, stopping the runtime — to anything that can reach the port. It is therefore gated by a shared bearer token.

```bash
nodered-mcp serve --transport http --http-addr :8090 --http-token "$(openssl rand -hex 32)"
```

Clients send it as an ordinary `Authorization` header:

```
Authorization: Bearer <token>
```

**The token is mandatory whenever the listen address is reachable from off the machine, and the server refuses to start without one.** The case this catches is `:8090`: it reads as local but binds every interface. A loopback bind — `127.0.0.1:8090`, `localhost:8090`, `[::1]:8090` — does not need a token, so local development stays frictionless.

```bash
nodered-mcp serve --transport http --http-addr :8090
# nodered-mcp: loading config: MCP_HTTP_TOKEN is required: ":8090" is reachable
# from outside this machine ...
```

Token comparison is constant-time, so a wrong guess reveals nothing about how much of it was right, and the token never appears in a response. Rejected requests are logged with the caller's address.

There is still no transport encryption here: over an untrusted network, put it behind a TLS-terminating reverse proxy. OAuth, which is what hosted web clients need, is not implemented — the MCP profile requires a full authorization server, not an extension of this.

## Client integration

All examples below use the stdio transport. For HTTP, see [the HTTP variant](#http-variant).

### Claude Desktop

**Recommended: the `.mcpb` extension.** A native installer that requires no JSON editing. Build the bundle with `scripts/build-mcpb.sh`, or download it from Releases once published, then open **Settings → Extensions → Install Extension** in Claude Desktop and select the `.mcpb` file. Claude Desktop presents a form for the Node-RED URL, token, and backup directory. The token is stored in the operating system credential store.

```bash
VERSION=v0.4.0 bash scripts/build-mcpb.sh   # requires go and npx
```

**Manual alternative.** Edit `claude_desktop_config.json` — `%APPDATA%\Claude\` on Windows, `~/Library/Application Support/Claude/` on macOS. See [`examples/claude_desktop_config.json`](./examples/claude_desktop_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "your-token-if-you-have-one"
      }
    }
  }
}
```

### Claude Code

```bash
claude mcp add nodered \
  -e NODERED_URL=http://localhost:1880 \
  -e NODERED_TOKEN=your-token \
  -- nodered-mcp
```

### Cursor

`.cursor/mcp.json` in your workspace, or `~/.cursor/mcp.json` globally. Same shape as the Claude Desktop snippet above. See [`examples/cursor_mcp.json`](./examples/cursor_mcp.json).

### VS Code

`.vscode/mcp.json` in your workspace. Note the root key is `servers`, not `mcpServers`. See [`examples/vscode_mcp.json`](./examples/vscode_mcp.json).

```json
{
  "servers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "your-token-if-you-have-one"
      }
    }
  }
}
```

### Gemini CLI

`~/.gemini/settings.json`. Same shape as the Claude Desktop snippet. See [`examples/gemini_settings.json`](./examples/gemini_settings.json).

### HTTP variant

Start the server once, then point the client at the endpoint rather than at a command. In clients that support `url` or `type: http`:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "headers": { "Authorization": "Bearer your-token" }
    }
  }
}
```

Drop the `headers` block only if the server is bound to loopback and running without a token.

Restart the client after connecting. All 29 tools should appear.

## Troubleshooting

**Tools do not appear.** Confirm the binary is on the `PATH`, or use an absolute path in `command`. On Windows, escape the backslashes: `C:\\path\\nodered-mcp.exe`. Running `nodered-mcp init` resolves the path for you.

**401 or 403 from Node-RED.** The token is missing or lacks the required scope. With `adminAuth` enabled, generate a token with write permission on flows.

**No log output.** Logs are written to stderr; stdout is reserved for JSON-RPC frames on the stdio transport. Increase verbosity with `--log-level debug`.

**HTTP transport does not connect.** Confirm `--transport http` is set and that the `--http-addr` port is free.

**A write is refused with a backup error.** Backups are fail-closed by design: if the snapshot cannot be written, the write does not proceed. Check that `NODERED_BACKUP_DIR` exists and is writable.

## Architecture

```
cmd/nodered-mcp/     entrypoint, CLI, init command
internal/config/     environment-variable loader and validation
internal/nodered/    HTTP client for the admin API
internal/mcp/        MCP server: tools, resources, prompts
```

The MCP layer is deliberately thin. Each client method maps to exactly one admin endpoint; the MCP layer decides how to expose those operations as tools.

Dependencies:

| Component | Choice |
|---|---|
| Language | Go 1.25+ |
| MCP SDK | [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — stdio and streamable HTTP |
| HTTP client | `net/http` (standard library) |
| WebSocket | [`coder/websocket`](https://github.com/coder/websocket) — the /comms debug stream. Zero dependencies of its own |
| Logging | `log/slog` (standard library) |
| Dev config | [`godotenv`](https://github.com/joho/godotenv) |

No frameworks, no ORM, no third-party Node-RED client.

## Development

```bash
go test ./...
go build -o nodered-mcp ./cmd/nodered-mcp
```

End-to-end check against a disposable instance:

```bash
docker run -it -p 1880:1880 nodered/node-red
go build -o nodered-mcp ./cmd/nodered-mcp
./nodered-mcp init --write
```

Then ask your client to list the Node-RED flows.

Work items are tracked in [`issues/`](./issues/README.md) until the repository moves to GitHub Issues. Design rationale lives in [`PLAN.md`](./PLAN.md).

## Roadmap

| Version | Scope | Status |
|---|---|---|
| v0.1 | 10 tools, 1 resource, 2 prompts, stdio transport | Released |
| v0.2 | Streamable HTTP transport, CLI with flags and subcommands | Released |
| v0.3 | Palette management: install, uninstall, enable, disable | Released |
| v0.4 | `search_nodes`, settings and runtime state — 19 tools, 3 resources, 2 prompts | Released |
| v0.5 | Read-only mode, context-efficient reads, diagnostics, context, the debug stream, granular node editing and `diff_flows` — 29 tools | Unreleased |
| v0.6 | Bearer auth on the HTTP transport | Unreleased |
| v0.7 | OAuth 2.1 for hosted web connectors | Planned |

## License

MIT. See [`LICENSE`](./LICENSE).

## Related project

[`nrcc`](https://github.com/fgjcarlos/nrcc) — Node-RED Control Center, a Go and React web dashboard for administering Node-RED instances. A separate codebase with no shared code, designed to coexist with this server.
