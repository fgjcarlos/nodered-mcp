# 201 — Tools: install/uninstall/list Node-RED nodes

**Labels:** feature, tools
**Milestone:** 3 — More tools

## Context

The Node-RED Admin API manages the palette directly — no shell/npm access needed:

- `GET /nodes` — list installed modules/sets
- `POST /nodes` `{"module":"node-red-dashboard","version":"3.6.0"}` — install from npm
- `DELETE /nodes/:module` — uninstall

Install/uninstall are mutating and slow (npm under the hood); surface errors verbatim (in-use nodes cannot be removed).

## Tools

- `list_nodes` — installed modules with name, version, enabled state
- `install_node` — args: `module` (required), `version` (optional)
- `uninstall_node` — args: `module` (required)

## Tasks

- [ ] Client methods in `internal/nodered/nodes.go` (extend the existing file)
- [ ] Tool registration + handlers in `internal/mcp/tools.go`, following the existing handler pattern
- [ ] Generous HTTP timeout for install (npm can take minutes) — justify wiring a per-call timeout here
- [ ] Tool descriptions warn that install/uninstall mutate the runtime
- [ ] httptest-based tests, same style as `client_test.go`

## Acceptance criteria

- From an MCP client: list palette, install a module by name, uninstall it; failures return the Node-RED error body
