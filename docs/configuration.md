# Configuration

**English** | [Español](es/configuration.md)

This page lists every setting `nodered-mcp` understands: where your Node-RED
is, where backups go, whether the assistant may change anything, and how the
network mode is secured. You need it when the defaults do not fit (Node-RED is
not on `localhost:1880`, it asks for a password, you want a safer read-only
setup) or when you want to look up what one variable does. Most people only
touch `NODERED_URL` and `NODERED_TOKEN`.

## Contents

- [How settings work](#how-settings-work)
- [Connecting to Node-RED](#connecting-to-node-red)
- [Backups](#backups)
- [Server behaviour](#server-behaviour)
- [HTTP transport](#http-transport)
- [Safety](#safety)

## How settings work

22 environment variables. Eleven of them also accept a command-line
flag; the rest are env-only. Precedence: **flag > env > default.**

An *environment variable* is a named setting passed to a program from outside,
for example in the `env` block of your MCP [client](glossary.md#client)
configuration (see [Client integration](clients.md)). A *flag* is the same
setting typed after the command, like `--url`. If you set both, the flag wins;
if you set neither, the default in the tables below applies.

In practice, these two are equivalent:

```bash
NODERED_URL=http://192.168.1.20:1880 nodered-mcp
nodered-mcp --url http://192.168.1.20:1880
```

To let the assistant only read and never change anything, set
`MCP_READ_ONLY=true` (or pass `--read-only`); see
[read-only mode](glossary.md#read-only-mode).

## Connecting to Node-RED

Tell the server where Node-RED lives and how to log in. If Node-RED has no
login enabled, the defaults work as they are. If it uses the
[admin API](glossary.md#admin-api) with authentication, give a token
(or, as an alternative, a username and password).

| Variable | Default | Flag | Description |
|---|---|---|---|
| `NODERED_URL` | `http://localhost:1880` | `--url` | Base URL of Node-RED |
| `NODERED_TOKEN` | *(empty)* | `--token` | Bearer [token](glossary.md#token) |
| `NODERED_USERNAME` | *(empty)* | — | Basic auth (alternative) |
| `NODERED_PASSWORD` | *(empty)* | — | Basic auth (alternative) |
| `NODERED_INSECURE` | `false` | — | Skip TLS verify |

### Tokens in the URL

`NODERED_URL` may carry a token in its query string
(`http://node-red:1880?token=…`). The MCP never echoes the query
string or the userinfo of that URL: a log line names the redacted URL,
and an HTTP error names the request path only —

```
nodered GET /flow/global: HTTP 404: Cannot GET /flow/global
```

A configuration error (an unparsable base URL, a URL that could not
be joined, a request that could not be built) names the reason it
failed on, not the URL it failed on.

The **body** of an error is the one Node-RED returned and is not
redacted: a runtime that echoes a secret in its response body puts it
in the error.

## Backups

| Variable | Default | Flag | Description |
|---|---|---|---|
| `NODERED_BACKUP_DIR` | `backups` | — | Where snapshots are written. Relative paths resolve against the MCP process's working directory. |
| `NODERED_BACKUP_KEEP` | `50` | — | How many backup files to retain. Older ones are pruned after each new snapshot; `0` disables pruning entirely |

When connecting to [several Node-RED instances](clients.md#several-node-red-instances),
set a different absolute backup directory for each MCP server. Servers that
share a directory can pick up one another's snapshots.

In the container image the backup directory is `/var/lib/nodered-mcp/backups`
(the image sets `NODERED_BACKUP_DIR` to it and runs as the `nonroot` user, so
the path is writable). The image ships that directory with the `nonroot`
user's ownership, so a volume mounted on it inherits that ownership. Mount
a separate volume per container at that path to keep snapshots when a
container is recreated without sharing backups across instances:

```bash
docker run --rm --name nodered-mcp \
  --volume nodered-backups:/var/lib/nodered-mcp/backups \
  ...
```

## Server behaviour

Log detail, which [transport](glossary.md#transport) to use, and
[read-only mode](glossary.md#read-only-mode).

| Variable | Default | Flag | Description |
|---|---|---|---|
| `MCP_LOG_LEVEL` | `info` | `--log-level` | debug / info / warn / error |
| `MCP_TRANSPORT` | `stdio` | `--transport` | `stdio` or `http` |
| `MCP_READ_ONLY` | `false` | `--read-only` | Register only the 21 read tools, none of the write tools |
| `MCP_DEBUG_STREAM` | `false` | `--debug-stream` | Open the `/comms` WebSocket at startup to enable the debug stream. **Off by default** because some Node-RED versions crash during the handshake (#17). After enabling it, `get_debug_messages` needs ~3s to start receiving messages |
| `MCP_LIST_FLOWS_FULL_THRESHOLD` | `200` | — | Node count above which `list_flows` with `detail="full"` refuses to dump the whole configuration unless the caller passes `force=true`. Guards the model's context window on large deployments |

## HTTP transport

Only consulted when `MCP_TRANSPORT=http` (see
[Transports](transports.md) and [streamable HTTP](glossary.md#streamable-http)).
Skip this section if your client starts `nodered-mcp` itself over
[stdio](glossary.md#stdio).

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

> **Per source IP means the socket address.** The limiter resolves the
> client from `RemoteAddr` and reads no request header. Behind a reverse
> proxy every client therefore shares the proxy's bucket — one noisy
> client can throttle everyone else. Enforce per-client limits at the
> proxy, where the real client address is known, and keep the MCP token
> or OAuth requirement. `X-Forwarded-For` is ignored deliberately:
> trusting it without an explicit trusted-proxy list is spoofable. See
> [Rate limiting behind a reverse proxy](../SECURITY.md#rate-limiting-behind-a-reverse-proxy).

## Safety

The [denylist](glossary.md#denylist) names node types the assistant is never
allowed to add to your flows.

| Variable | Default | Flag | Description |
|---|---|---|---|
| `MCP_NODE_DENYLIST` | `exec,system` | — | Node types every write that can deploy a node type refuses, from `add_node` to `import_flow`, `set_flows` and `restore_backup`. These two run shell commands as the Node-RED process, so allowing them turns the write tools into remote code execution. Set to the empty string to opt out — see [`SECURITY.md`](../SECURITY.md) |

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