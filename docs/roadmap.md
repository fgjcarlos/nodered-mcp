# Roadmap

## Open risks & decisions

| Topic | Status |
|---|---|
| Data model with field loss | ✅ Resolved: JSON-opaque |
| Backup before write | ✅ Implemented, fail-closed |
| LLM editing strategy | ✅ Resolved in v0.5: granular by default, full rewrite as escape hatch |
| `/comms` WebSocket | ✅ Implemented with reconnect and bounded buffer |
| HTTP transport alongside stdio | ✅ Implemented |
| Bearer auth on HTTP transport | ✅ Token mandatory on non-loopback binds; server refuses to start without it |
| OAuth for web clients | ✅ OAuth 2.1 / OIDC resource server with JWKS discovery |
| **Concurrency (two LLMs editing)** | ⬜ **Unresolved: last-write-wins.** Granular edits do read-modify-write, so the window is narrow but not gone |
| **Backup pruning** | ⬜ **Pending** (marked with `ponytail:` in `backup.go`). Add when the directory gets unwieldy |
| Debug buffer size | ⬜ Fixed at 500; configurable if someone hits the ceiling |
| Local query cache | ⬜ Not planned |

## Delivered versions

| Version | Scope | Status |
|---|---|---|
| v0.1 | 10 tools, 1 resource, 2 prompts, stdio transport | Released |
| v0.2 | Streamable HTTP transport, CLI with flags and subcommands | Released |
| v0.3 | Palette management: install, uninstall, enable, disable | Released |
| v0.4 | `search_nodes`, settings and runtime state — 19 tools, 3 resources, 2 prompts | Released |
| v0.5 | Read-only mode, context-efficient reads, diagnostics, context, the debug stream, granular node editing, `diff_flows`, HTTP bearer auth — 43 tools | Released |
| v0.6 | OAuth 2.1 Resource Server for hosted web connectors; npm channel with Windows binaries; SBOM + provenance on every artefact; Dependabot; SHA-pinned actions; CODEOWNERS + issue/PR templates; central NR version detection; get_runtime_info with capability matrix; startup banner with degraded-tools count — 44 tools | Released |

## Planned

| Version | Scope |
|---|---|
| v0.7 | Local query cache |