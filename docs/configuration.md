# Configuration

22 environment variables. Eleven of them also accept a command-line
flag; the rest are env-only. Precedence: **flag > env > default.**

## Connecting to Node-RED

| Variable | Default | Flag | Description |
|---|---|---|---|
| `NODERED_URL` | `http://localhost:1880` | `--url` | Base URL of Node-RED |
| `NODERED_TOKEN` | *(empty)* | `--token` | Bearer token |
| `NODERED_USERNAME` | *(empty)* | — | Basic auth (alternative) |
| `NODERED_PASSWORD` | *(empty)* | — | Basic auth (alternative) |
| `NODERED_INSECURE` | `false` | — | Skip TLS verify |

## Backups

| Variable | Default | Flag | Description |
|---|---|---|---|
| `NODERED_BACKUP_DIR` | `backups` | — | Where snapshots are written |
| `NODERED_BACKUP_KEEP` | `50` | — | How many backup files to retain. Older ones are pruned after each new snapshot; `0` disables pruning entirely |

## Server behaviour

| Variable | Default | Flag | Description |
|---|---|---|---|
| `MCP_LOG_LEVEL` | `info` | `--log-level` | debug / info / warn / error |
| `MCP_TRANSPORT` | `stdio` | `--transport` | `stdio` or `http` |
| `MCP_READ_ONLY` | `false` | `--read-only` | Register only the 21 read tools, none of the write tools |
| `MCP_DEBUG_STREAM` | `false` | `--debug-stream` | Open the `/comms` WebSocket at startup to enable the debug stream. **Off by default** because some Node-RED versions crash during the handshake (#17). After enabling it, `get_debug_messages` needs ~3s to start receiving messages |
| `MCP_LIST_FLOWS_FULL_THRESHOLD` | `200` | — | Node count above which `list_flows` with `detail="full"` refuses to dump the whole configuration unless the caller passes `force=true`. Guards the model's context window on large deployments |

## HTTP transport

Only consulted when `MCP_TRANSPORT=http`.

| Variable | Default | Flag | Description |
|---|---|---|---|
| `MCP_HTTP_ADDR` | `:8090` | `--http-addr` | Listen address for the http transport |
| `MCP_HTTP_TOKEN` | *(empty)* | `--http-token` | Bearer token for the http transport. **Mandatory** if the address is not loopback |
| `MCP_HTTP_MAX_BODY` | `33554432` (32 MiB) | — | Cap on a single request body, in bytes. Enforced before any handler runs, so an oversized POST cannot exhaust memory |
| `MCP_HTTP_RATE_PER_SEC` | `1.0` | — | Steady-state requests per second allowed per source IP |
| `MCP_HTTP_RATE_BURST` | `10` | — | How many requests a single source IP may burst before throttling |
| `MCP_HTTP_RATE_DISABLED` | `false` | — | Turn the rate limiter off entirely. For tests, sandboxes, or when throttling is handled at another layer |
| `MCP_ALLOW_INSECURE_LOOPBACK` | `false` | `--allow-insecure-loopback` | Silence the startup warning about a loopback bind with no auth. **Adds no authentication** — it only acknowledges the trade-off |
| `MCP_OAUTH_ISSUER` | *(empty)* | `--oauth-issuer` | Enable OAuth 2.1 / OIDC on the HTTP transport. Mutually exclusive with `MCP_HTTP_TOKEN` |
| `MCP_OAUTH_AUDIENCE` | *(empty)* | `--oauth-aud` | Audience claim required when an issuer is set |

## Safety

| Variable | Default | Flag | Description |
|---|---|---|---|
| `MCP_NODE_DENYLIST` | `exec,system` | — | Node types the write tools refuse to create. These two run shell commands as the Node-RED process, so allowing them turns `add_node` into remote code execution. Set to the empty string to opt out — see [`SECURITY.md`](../SECURITY.md) |

`--http-addr :8090` **is not** loopback: it listens on every
interface. That is why the token is mandatory there.

`nodered-mcp version` prints the version. If installed via
`go install …@latest`, the visible version is the resolved git tag
because `resolveVersion` (`cmd/nodered-mcp/main.go`) recovers it
from the toolchain's build info.

For local development, a `.env` file in the working directory is
loaded automatically. Environment variables already set always win.

```bash
cp .env.example .env
```