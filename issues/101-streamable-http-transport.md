# 101 — Streamable HTTP transport

**Labels:** feature, transport
**Milestone:** 2 — Reach

## Context

Today the server only speaks stdio, which every MCP client supports but forces a local binary per client. Streamable HTTP lets one running instance serve remote/multiple clients (Claude Code `--transport http`, VS Code, Cursor, web-based hosts) and is the path to running next to Node-RED on the same box or container.

`mark3labs/mcp-go` ships `server.NewStreamableHTTPServer` — no new dependency needed.

This issue supersedes the audit finding "remove `MCP_TRANSPORT`": instead of deleting the config, make it real.

## Tasks

- [ ] Extend `Config.MCPTransport` validation to accept `"http"`; add `MCP_HTTP_ADDR` (default `:8090`)
- [ ] In `main.go` / `internal/mcp/server.go`, branch on transport: `ServeStdio` vs `NewStreamableHTTPServer(...).Start(addr)`
- [ ] Graceful shutdown on SIGINT/SIGTERM for the HTTP path
- [ ] Document both transports in README with a sample client config each
- [ ] Tests for config validation of the new values

## Acceptance criteria

- `MCP_TRANSPORT=http MCP_HTTP_ADDR=:8090` serves MCP over streamable HTTP; default remains stdio
- An MCP client connecting over HTTP can list and call the existing tools

## Non-goals

- Auth on the HTTP endpoint (separate issue if ever exposed beyond localhost)
- Legacy SSE transport
