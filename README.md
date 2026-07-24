# nodered-mcp

> An **MCP (Model Context Protocol)** server in Go that exposes the Node-RED admin API as tools, resources, and prompts for AI clients (Claude Desktop, Cursor, Cline, etc.).

```
   Claude Desktop  ───stdin/stdout───▶  nodered-mcp  ───HTTP───▶  Node-RED :1880
```

A Spanish version of this README is available at [`README.es.md`](./README.es.md).

## What it does

Gives your LLM the ability to:

- 📋 **List, read, create, update, and delete** flows (`list_flows`, `get_flow`, `create_flow`, `update_flow`, `delete_flow`)
- ⚡ **Trigger inject nodes** on demand without opening the editor (`inject_node`)
- 🧩 **Inspect the palette**: what's installed and what each node does (`list_nodes`, `get_node_info`)
- 📦 **Manage the palette**: install, uninstall, and enable/disable node modules (`install_node`, `uninstall_node`, `enable_node`, `disable_node`)
- 🔎 **Search the npm catalog** before installing (`search_nodes`)
- ⚙️ **Diagnose the server**: read its config and runtime state (`get_settings`, `get_flows_state`, `set_flows_state`, `set_flows`)
- 🛟 **Recover changes**: automatic backups before every write + `list_backups` / `restore_backup`
- 🧠 **Receive reusable prompts** (`explain_flow`, `generate_flow`) that start with the right context

More details in [`PLAN.md`](./PLAN.md).

## Installation

> **nodered-mcp is a generic MCP server.** It does not depend on any AI provider: the same binary works with any client that speaks MCP — Claude, Cursor (with GPT or whichever model you use), Gemini CLI, VS Code, Cline… Pick an install method, then [connect your client](#connect-your-mcp-client).

Works on **Linux, macOS, and Windows** (amd64 and arm64).

### Option A — Prebuilt binary (recommended, no Go required)

Download your system's binary from [Releases](https://github.com/fgjcarlos/nodered-mcp/releases) and put it on the PATH.

Linux / macOS:

```bash
# adjust the file name to your OS/architecture
curl -sSL https://github.com/fgjcarlos/nodered-mcp/releases/latest/download/nodered-mcp_Linux_x86_64.tar.gz | tar xz
sudo mv nodered-mcp /usr/local/bin/
nodered-mcp version
```

Windows (PowerShell): download the `.zip`, extract it, and move `nodered-mcp.exe` into a folder on the `PATH`.

### Option B — Docker (ideal for the HTTP transport)

```bash
docker build -t nodered-mcp .
docker run --rm -p 8090:8090 \
  -e NODERED_URL=http://host.docker.internal:1880 \
  -e NODERED_TOKEN=your-token \
  nodered-mcp
# MCP endpoint: http://localhost:8090/mcp
```

On Linux, if Node-RED runs on the host, add `--add-host=host.docker.internal:host-gateway` to the `docker run`. The image starts on the **http** transport by default (stdio makes no sense inside a container).

### Option C — `go install` (if you already have Go)

```bash
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
# lands in $(go env GOPATH)/bin — make sure that is on your PATH
```

### Option D — From source

```bash
git clone https://github.com/fgjcarlos/nodered-mcp
cd nodered-mcp
go build -o nodered-mcp ./cmd/nodered-mcp
```

> **Current status:** options B and D work today. Options A and C require the repo to be published on GitHub with at least one `vX.Y.Z` tag (see [`issues/301`](./issues/301-publish-repo.md)). Until then, use Docker or build from source.

## Configuration

Every knob can be passed as an env var or a flag. Flags override env vars, env vars override defaults.

```bash
cp .env.example .env
# edit .env
```

| Variable | Flag | Default | Description |
|---|---|---|---|
| `NODERED_URL` | `--url` | `http://localhost:1880` | Node-RED base URL |
| `NODERED_TOKEN` | `--token` | — | Bearer token (when admin auth is enabled) |
| `NODERED_USERNAME` / `NODERED_PASSWORD` | — | — | Basic auth, alternative to the token |
| `NODERED_INSECURE` | — | `false` | Skip TLS verification (dev only) |
| `NODERED_BACKUP_DIR` | — | `backups` | Where to write flow snapshots before each write |
| `MCP_TRANSPORT` | `--transport` | `stdio` | `stdio` or `http` |
| `MCP_HTTP_ADDR` | `--http-addr` | `:8090` | Listen address for the `http` transport |
| `MCP_LOG_LEVEL` | `--log-level` | `info` | `debug` \| `info` \| `warn` \| `error` |

### CLI

```bash
nodered-mcp                # start the server (equivalent to `nodered-mcp serve`)
nodered-mcp serve --help   # list every flag
nodered-mcp init           # generate a config snippet for your MCP client
nodered-mcp version        # print the version
```

`init` detects which MCP clients you have installed, asks for the Node-RED URL/port, the token and the backup folder, and **autodetects the binary path** — so the snippet never points to a non-existent executable (the typical cause of "Server disconnected").

- `nodered-mcp init` → prints the snippet to paste.
- `nodered-mcp init --write` → **writes directly** into the client's config (safe merge that preserves your other servers; saves a `.bak` of the previous file). Supported for Claude Desktop, Cursor, and Gemini CLI; for VS Code (workspace-scoped config) and Claude Code (`claude mcp add`) it prints the instruction.
- `nodered-mcp init --all` → shows every known client, not just the detected ones.

## Transports

- **stdio** (default): the MCP client launches the binary and talks over stdin/stdout. What Claude Desktop, Cursor, VS Code, etc. use.
- **http** (streamable HTTP): a single process serves several remote clients. The MCP endpoint is at `<addr>/mcp`.

```bash
nodered-mcp serve --transport http --http-addr :8090
# endpoint: http://localhost:8090/mcp
```

> The `http` transport has no built-in auth yet. Do not expose it outside localhost / a trusted network.

## Connect your MCP client

Every example below uses **stdio**. For HTTP, see the [HTTP variant](#http-variant) at the bottom.

### Claude Desktop

**One-click option (recommended): the `.mcpb` extension.** A native installer — no JSON editing. Generate the bundle with `scripts/build-mcpb.sh` (or download it from Releases once published), and in Claude Desktop go to **Settings → Extensions → Install Extension** and pick the `.mcpb`. Claude Desktop shows a form asking for the Node-RED URL/port, the token, and the backup folder. The token is stored encrypted in the Windows Credential Manager.

```bash
# produces nodered-mcp-<os>-<arch>.mcpb (needs go + npx)
VERSION=v0.4.0 bash scripts/build-mcpb.sh
```

**Manual option: edit the config.** `%APPDATA%\Claude\claude_desktop_config.json` on Windows (or its equivalent on your OS). See [`examples/claude_desktop_config.json`](./examples/claude_desktop_config.json):

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
claude mcp add nodered -e NODERED_URL=http://localhost:1880 -e NODERED_TOKEN=your-token -- nodered-mcp
```

### VS Code

`.vscode/mcp.json` in your workspace. See [`examples/vscode_mcp.json`](./examples/vscode_mcp.json):

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

### Cursor

`.cursor/mcp.json` in your workspace (or `~/.cursor/mcp.json` globally). See [`examples/cursor_mcp.json`](./examples/cursor_mcp.json):

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

### Gemini CLI

`~/.gemini/settings.json`. See [`examples/gemini_settings.json`](./examples/gemini_settings.json):

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

### HTTP variant

Start the server once (`nodered-mcp serve --transport http --http-addr :8090`) and point the client at the endpoint instead of a command. In clients that support `url` / `type: http`:

```json
{
  "mcpServers": {
    "nodered": { "url": "http://localhost:8090/mcp" }
  }
}
```

After connecting, restart the client. You will see the 19 tools available.

## Troubleshooting

- **Tools don't appear:** make sure the binary is on the `PATH`, or use an absolute path in `command`. On Windows, escape the backslashes (`C:\\path\\nodered-mcp.exe`).
- **401 / 403 from Node-RED:** the token is missing or lacks scope. With `adminAuth` enabled, generate a token with permissions to write flows.
- **The server logs nothing:** logs go to **stderr** (stdout is reserved for the protocol). Turn up the detail with `--log-level debug`.
- **HTTP doesn't connect:** confirm `--transport http` is set and the `--http-addr` port is free.

## Example

Once connected, in Claude Desktop:

> **You:** "List the flows I have in Node-RED."
> **Claude:** *(invokes `list_flows`)* "You have 3 flows: `Home`, `MQTT Bridge`, and `Weather`. Want me to open one?"

> **You:** "Add an inject node to the `Home` flow that fires every 5 seconds with payload `hello`."
> **Claude:** *(reads the flow, proposes the change, applies it with `update_flow`)*

## Architecture

```
cmd/nodered-mcp/        # entrypoint
internal/config/        # env-var loader
internal/nodered/       # HTTP client against the admin API
internal/mcp/           # MCP server (tools, resources, prompts)
```

Stack:

- Go 1.25+
- [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — MCP SDK (stdio + streamable HTTP)
- `net/http` (stdlib) — Node-RED client
- `log/slog` (stdlib) — logging
- [`godotenv`](https://github.com/joho/godotenv) — `.env` in dev

## Tests

```bash
go test ./...
```

## Roadmap

- **v0.1:** 10 tools, 1 resource, 2 prompts, stdio
- **v0.2:** HTTP (streamable) transport + CLI with flags ✅
- **v0.3:** install/uninstall/enable/disable nodes ✅
- **v0.4:** search_nodes + get/set settings + flows state (19 tools, 3 resources, 2 prompts) ✅
- **v0.5:** bearer auth on the HTTP transport

See [`PLAN.md`](./PLAN.md) for the full breakdown.

## License

MIT

## Sister project

[`nrcc`](https://github.com/fgjcarlos/nrcc) — Node-RED Control Center, a Go + React web dashboard for administering Node-RED instances. A separate project, no shared code, but designed to coexist.
