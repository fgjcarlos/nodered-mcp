# Transports

## stdio (default)

The MCP client launches the binary and communicates over stdin /
stdout. This is what Claude Desktop, Claude Code, Cursor, VS Code,
and Gemini CLI use.

## http (streamable HTTP)

A single long-running process serves several clients. The MCP
endpoint is at `<addr>/mcp`.

```bash
nodered-mcp serve --transport http --http-addr :8090
```

### Authenticating the HTTP transport

The HTTP transport exposes every tool — deploying flows, installing
modules, stopping the runtime — to anything that can reach the port.
It is therefore gated by a shared bearer token.

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
network, put it behind a TLS-terminating reverse proxy. OAuth, which
is what hosted web clients need, is not implemented — the MCP
profile requires a full authorization server, not an extension of
this.

## OAuth 2.1 (alternative to the bearer token)

Web connectors that already speak OAuth 2.1 / OpenID Connect
(claude.ai, custom front-ends) can identify themselves with a JWT
instead of a shared secret. Configure the issuer and audience, drop
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

Configuring both `MCP_HTTP_TOKEN` and `MCP_OAUTH_ISSUER` is a
configuration error and the server refuses to start.