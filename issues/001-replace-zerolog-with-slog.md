# 001 — Replace zerolog with log/slog

**Labels:** cleanup, dependencies
**Milestone:** 1 — Cleanup

## Context

The only logging usage is leveled + structured calls (`Debug/Info/Error` + `Str/Bool` fields). `log/slog` (stdlib since Go 1.21) covers this with the same API shape. Dropping zerolog also drops three indirect deps: `mattn/go-colorable`, `mattn/go-isatty`, `golang.org/x/sys`.

Logs MUST keep going to stderr — stdout is the MCP stdio channel.

## Tasks

- [ ] Replace zerolog calls with `slog` in `cmd/nodered-mcp/main.go`, `internal/config/config.go`, `internal/nodered/client.go`, `internal/nodered/backup.go`, `internal/mcp/server.go`, `internal/mcp/tools.go`
- [ ] Configure a `slog.NewTextHandler(os.Stderr, ...)` default logger with level from `LOG_LEVEL`
- [ ] `go mod tidy` — zerolog and its indirects gone
- [ ] `go test ./...` passes

## Acceptance criteria

- No `zerolog` import remains; `go.mod` has only `godotenv` and `mcp-go` as direct deps
- Log output still lands on stderr with levels honored
