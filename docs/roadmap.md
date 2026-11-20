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
| **Concurrency (two LLMs editing)** | ✅ Resolved: a granular edit is built on the revisioned snapshot it deploys, so a deployment that lands in between is refused with a 409 instead of overwriting it |
| **Backup pruning** | ✅ Implemented: the newest `NODERED_BACKUP_KEEP` snapshots are kept (default 50, `0` disables pruning) |
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
| v0.7 | npm-native platform packages: the main npm package lists the six native binaries as exact-version `optionalDependencies` and the postinstall downloader is gone; npm publication hardened (scope guard, serialized promotion, pinned release toolchain); `init` detects Claude Desktop before its config exists | Released (v0.7.2) |
| v0.8 | Hardening: the node denylist on every write surface, deployments validated before they reach the runtime, the editor's flat clipboard in `export_flow`/`import_flow` (breaking), no replay of mutating tools, granular edits on one revisioned snapshot, cancelable write lock, bounded JWKS refreshes, every publication channel gated on the tagged commit's CI; `init --dry-run` | Released (v0.8.0) |

## Planned

No version is planned yet. The local query cache was considered for v0.7
and dropped: the risk table above lists it as *not planned*, and v0.7
shipped the npm-native platform packages instead.
