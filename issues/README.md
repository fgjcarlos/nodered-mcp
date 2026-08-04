# Issues Index

Local issue tracker. One file per issue. Move to GitHub Issues when the repo goes public.

## Milestone 1 — Cleanup (from over-engineering audit) — ✅ DONE

| # | Title | Effort | Status |
|---|-------|--------|--------|
| [001](001-replace-zerolog-with-slog.md) | Replace zerolog with log/slog | S | ✅ |
| [002](002-remove-committed-binaries.md) | Remove committed build binaries | XS | ✅ |
| [003](003-dedupe-pretty-json.md) | Deduplicate JSON pretty-printing | XS | ✅ |
| [004](004-remove-unused-mcpserver-getter.md) | Remove unused MCPServer() getter | XS | ✅ |
| [005](005-remove-unused-timeout-option.md) | Remove or wire Options.Timeout | XS | ✅ (option A) |
| [006](006-gitignore-runtime-dirs.md) | Ignore backups/ and .atl/ | XS | ✅ |

## Milestone 2 — Reach (more providers, easier install) — ✅ DONE

| # | Title | Effort | Status |
|---|-------|--------|--------|
| [101](101-streamable-http-transport.md) | Streamable HTTP transport | M | ✅ |
| [102](102-cli.md) | CLI: flags + subcommands | S | ✅ |
| [103](103-provider-setup-docs.md) | Setup docs/configs per MCP client | S | ✅ |

## Milestone 3 — More tools (Node-RED node management) — ✅ DONE

| # | Title | Effort | Status |
|---|-------|--------|--------|
| [201](201-node-install-tools.md) | Tools: install/uninstall/list nodes | M | ✅ |
| [202](202-node-enable-disable.md) | Tools: enable/disable node sets | S | ✅ |
| [203](203-catalog-search-tool.md) | Tool: search the Node-RED catalog | S | ✅ (uses npm registry, supports private mirror via `Options.SearchBaseURL`) |

## Milestone 4 — Distribution / DX (easy install, any provider, any OS)

| # | Title | Effort | Status |
|---|-------|--------|--------|
| [301](301-publish-repo.md) | Publish the repo on GitHub | S | ⛔ blocker — needs maintainer's GitHub (can't automate) |
| [302](302-goreleaser-binaries.md) | Prebuilt binaries via GoReleaser | M | ✅ (fires once 301 lands) |
| [303](303-dockerfile.md) | Dockerfile for HTTP transport | S | ✅ (built + verified, ~18.7 MB) |
| [304](304-quickstart-rewrite.md) | Rewrite install/Quick Start | S | ✅ |
| [305](305-mcpb-bundle.md) | Claude Desktop one-click install (.mcpb) | M | 🗑️ retired — the project ships three channels (npm, go, docker) and nothing else |
| [306](306-init-command.md) | `init`: interactive universal config generator | S | ✅ (built + verified; `--write` auto-configures) |
| [307](307-install-scripts.md) | One-line install scripts (curl/irm) | S | ✅ written (inert until 301) |

## Order

Milestone 1 first (small, unblocks clean diffs). 101 before 102 (the CLI exposes the transport flag). 201 before 202/203.
