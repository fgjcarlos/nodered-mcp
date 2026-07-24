# 005 — Remove or wire Options.Timeout

**Labels:** cleanup, yagni
**Milestone:** 1 — Cleanup

## Context

`nodered.Options.Timeout` (`internal/nodered/client.go:78-81`) is never set by `config.Load` or `main.go` — there is no `NODERED_TIMEOUT` env var. Only tests use it. Dead flexibility as shipped.

Pick ONE:

- **A (lean, default):** delete the field, hardcode the 30s default.
- **B:** keep the field and wire a `NODERED_TIMEOUT` env var in `internal/config`. Only do this if slow Node-RED instances are a real, observed problem.

## Tasks

- [ ] Apply option A (or B with justification in the commit message)
- [ ] Adjust tests

## Acceptance criteria

- No config surface that nothing can set; `go test ./...` passes
