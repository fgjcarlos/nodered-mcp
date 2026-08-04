# Configuration

13 environment variables. Each one also accepts a command-line flag.
Precedence: **flag > env > default.**

| Variable | Default | Flag | Description |
|---|---|---|---|
| `NODERED_URL` | `http://localhost:1880` | `--url` | Base URL of Node-RED |
| `NODERED_TOKEN` | *(empty)* | `--token` | Bearer token |
| `NODERED_USERNAME` | *(empty)* | — | Basic auth (alternative) |
| `NODERED_PASSWORD` | *(empty)* | — | Basic auth (alternative) |
| `NODERED_INSECURE` | `false` | — | Skip TLS verify |
| `NODERED_BACKUP_DIR` | `backups` | — | Where snapshots are written |
| `MCP_LOG_LEVEL` | `info` | `--log-level` | debug / info / warn / error |
| `MCP_TRANSPORT` | `stdio` | `--transport` | `stdio` or `http` |
| `MCP_HTTP_ADDR` | `:8090` | `--http-addr` | Listen address for the http transport |
| `MCP_HTTP_TOKEN` | *(empty)* | `--http-token` | Bearer token for the http transport. **Mandatory** if the address is not loopback |
| `MCP_READ_ONLY` | `false` | `--read-only` | Register only the 20 read tools, none of the write tools |
| `MCP_DEBUG_STREAM` | `false` | `--debug-stream` | Open the `/comms` WebSocket at startup to enable the debug stream. **Off by default** because some Node-RED versions crash during the handshake (#17). After enabling it, `get_debug_messages` needs ~3s to start receiving messages |
| `MCP_OAUTH_ISSUER` | *(empty)* | `--oauth-issuer` | Enable OAuth 2.1 / OIDC on the HTTP transport |
| `MCP_OAUTH_AUDIENCE` | *(empty)* | `--oauth-aud` | Audience claim required when an issuer is set |

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