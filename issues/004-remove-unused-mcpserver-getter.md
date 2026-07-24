# 004 — Remove unused MCPServer() getter

**Labels:** cleanup, yagni
**Milestone:** 1 — Cleanup

## Context

`Server.MCPServer()` (`internal/mcp/server.go:59-61`) has zero call sites. It was kept "for testing and future transports". Issue 101 (HTTP transport) will add access if and when it actually needs it.

## Tasks

- [ ] Delete the method

## Acceptance criteria

- `go build ./...` and `go test ./...` pass with the method gone
