# 003 — Deduplicate JSON pretty-printing

**Labels:** cleanup
**Milestone:** 1 — Cleanup

## Context

`json.Indent` with a raw-bytes fallback is implemented three times. The helper `prettyJSON()` already exists in `internal/mcp/tools.go:348`; two inline copies remain in the same package.

## Tasks

- [ ] `handleListFlows` (`internal/mcp/tools.go:163-168`) → call `prettyJSON(raw)`
- [ ] `handleFlowsResource` (`internal/mcp/resources.go:41-45`) → call `prettyJSON(raw)`

## Acceptance criteria

- Exactly one `json.Indent` call site in package `mcp`; `go test ./...` passes
