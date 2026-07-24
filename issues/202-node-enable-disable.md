# 202 — Tools: enable/disable node sets

**Labels:** feature, tools
**Milestone:** 3 — More tools

## Context

`PUT /nodes/:module` and `PUT /nodes/:module/:set` with `{"enabled":bool}` toggle modules/sets without uninstalling — useful for debugging a broken palette. Cheap once 201 lands.

## Tasks

- [ ] Client method `SetNodeEnabled(module, set string, enabled bool)` in `internal/nodered/nodes.go`
- [ ] Tools `enable_node` / `disable_node` (args: `module`, optional `set`)
- [ ] Tests

## Acceptance criteria

- Toggling a module flips its `enabled` state in `list_nodes` output
- Depends on: 201
