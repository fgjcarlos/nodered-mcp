# Transports

**English** | [Español](es/transports.md)

A [transport](glossary.md#transport) is the channel the AI app and
`nodered-mcp` use to talk to each other. This page explains the two channels
available, which one you should pick, and how to protect the network one. You
only need it if you want to share one server between several apps or
computers, or if you are curious how the connection works; otherwise the
default is fine.

## Contents

- [Which transport do I need?](#which-transport-do-i-need)
- [stdio (default)](#stdio-default)
- [http (streamable HTTP)](#http-streamable-http)
  - [Authenticating the HTTP transport](#authenticating-the-http-transport)
- [OAuth 2.1 (alternative to the bearer token)](#oauth-21-alternative-to-the-bearer-token)

## Which transport do I need?

| If you... | Use |
|---|---|
| run one AI app on the same computer as `nodered-mcp` | stdio (default, nothing to set up) |
| want one long-running server shared by several apps or machines | http |

In practice: with stdio your client config says `"command": "nodered-mcp"`
and the app starts the server for you; with http you start the server
yourself and the client config says `"url": "http://localhost:8090/mcp"`.
See [Client integration](clients.md) for both shapes. The setting is
`MCP_TRANSPORT` / `--transport` (see [Configuration](configuration.md)).

## stdio (default)

The MCP [client](glossary.md#client) launches the binary and communicates over
stdin / stdout (the program's plain text input and output, with no network
involved; see [stdio](glossary.md#stdio)). This is what Claude Desktop,
Claude Code, Cursor, VS Code, and Gemini CLI use.

## http (streamable HTTP)

A single long-running process serves several clients
([streamable HTTP](glossary.md#streamable-http) is the MCP name for this
network mode). The MCP endpoint is at `<addr>/mcp`.

The endpoint supports the streaming half of Streamable HTTP: a `GET`
with `Accept: text/event-stream` opens a listening stream for server
notifications, and responses may be sent as Server-Sent Events. A
reverse proxy in front of the server must not buffer
`text/event-stream` responses (for nginx, `proxy_buffering off`), or
events arrive late or not at all.

```bash
nodered-mcp serve --transport http --http-addr :8090
```

### Authenticating the HTTP transport

The HTTP transport exposes every tool — deploying flows, installing
modules, stopping the runtime — to anything that can reach the port.
It is therefore gated by a shared bearer [token](glossary.md#token): a secret
string that the client sends with each request and the server checks.

```bash
nodered-mcp serve --transport http --http-addr :8090 --http-token "$(openssl rand -hex 32)"
```

Clients send it as an ordinary `Authorization` header:

```
Authorization: Bearer ***
```

**The token is mandatory whenever the listen address is reachable
from off the machine, and the server refuses to start without one.**
The case this catches is `:8090`: it reads as local but binds every
interface. A loopback bind — `127.0.0.1:8090`, `localhost:8090`,
`[::1]:8090` — does not need a token, so local development stays
frictionless.

```bash
nodered-mcp serve --transport http --http-addr :8090
# nodered-mcp: loading config: MCP_HTTP_TOKEN is required: ":8090" is reachable
# from outside this machine ...
```

Token comparison is constant-time, so a wrong guess reveals nothing
about how much of it was right, and the token never appears in a
response. Rejected requests are logged with the caller's address.

There is still no transport encryption here: over an untrusted
network, put it behind a TLS-terminating reverse proxy. Hosted web
clients that need OAuth instead of a fixed token are covered in the
next section.

## OAuth 2.1 (alternative to the bearer token)

Use this instead of the bearer token only if your organisation already runs
an identity provider. Web connectors that already speak OAuth 2.1 / OpenID
Connect (claude.ai, custom front-ends) can identify themselves with a JWT
(a signed token issued by that provider) instead of a shared secret. Configure the issuer and audience, drop
the bearer token:

```bash
MCP_TRANSPORT=http
MCP_HTTP_ADDR=:8090
MCP_OAUTH_ISSUER=https://your-idp.example/
MCP_OAUTH_AUDIENCE=nodered-mcp
```

On startup the server fetches
`<issuer>/.well-known/openid-configuration` to discover the JWKS
endpoint, then verifies every request's `Authorization: Bearer ***`
against the issuer's signing keys. `iss` must match the configured
issuer; `aud` must match the configured audience. Discovery happens
once at boot, so a misconfigured URL fails fast rather than on the
first authenticated call.

The signing keys are cached for 15 minutes. A token whose `kid` is
not in the cache triggers one extra fetch, because the identity
provider may have rotated; that extra fetch happens at most once per
minute and is shared by the requests that miss at the same time, so a
burst of invalid tokens cannot become a burst of downloads against the
identity provider. A rotated key is therefore picked up as soon as the
window opens, which can be up to a minute after the rotation.

The window is per process, so a fleet of replicas allows one refresh
per replica and minute. While the identity provider is unreachable and
the cache has expired, every request that arrives after a failed fetch
tries again on its own; the requests that were waiting when it failed
share that outcome instead of queueing behind it.

Configuring both `MCP_HTTP_TOKEN` and `MCP_OAUTH_ISSUER` is a
configuration error and the server refuses to start.