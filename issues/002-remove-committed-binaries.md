# 002 — Remove committed build binaries

**Labels:** cleanup
**Milestone:** 1 — Cleanup

## Context

`nodered-mcp.exe` and `nodered-mcp.exe~` (~12 MB each) sit in the repo root. They are gitignored but still present in the working tree.

## Tasks

- [ ] Delete `nodered-mcp.exe` and `nodered-mcp.exe~`
- [ ] Confirm `.gitignore` covers `*.exe` and `*.exe~`

## Acceptance criteria

- No binaries in the tree; a fresh `go build ./cmd/nodered-mcp` still works
