# 305 — Claude Desktop one-click install (.mcpb bundle)

**Labels:** distribution, dx, claude-desktop
**Milestone:** 4 — Distribution / DX
**Status:** ✅ built + validated (one-click install still needs a real Claude Desktop to confirm end-to-end)

## Context

Claude Desktop supports **Desktop Extensions** (`.mcpb` bundles): a zip with the binary + a `manifest.json`. Installed in one click via Settings → Extensions. When the manifest declares a `user_config` section, Claude Desktop **auto-generates a settings form** asking for those values — exactly the "installer that asks for the port and backup folder" we want. No hand-edited JSON, no stale paths. Supports binary MCP servers, so our Go `.exe` fits directly. Sensitive fields (token) are stored in the OS credential store.

Claude-Desktop-specific: other clients have their own one-click mechanisms (issue 306 covers the universal fallback).

## What was done

- [x] `mcpb/manifest.json` (`manifest_version` 0.3, `server.type: binary`, `command: ${__dirname}/server/nodered-mcp` — `.exe` auto-appended on Windows). Validated with `mcpb validate` → schema passes.
- [x] `user_config` fields mapped to env vars via `${user_config.*}`:
  - `nodered_url` → `NODERED_URL` (string, default `http://localhost:1880`, required)
  - `nodered_token` → `NODERED_TOKEN` (string, sensitive, optional)
  - `backup_dir` → `NODERED_BACKUP_DIR` (directory, default `${HOME}/nodered-mcp-backups`)
- [x] `scripts/build-mcpb.sh` — cross-platform build + `mcpb pack` (`GOOS`/`GOARCH` overridable). Produced `nodered-mcp-windows-amd64.mcpb` (3.7 MB, manifest + binary).
- [x] README: one-click install section (Settings → Extensions → Install Extension → fill the form).

## Remaining

- [ ] End-to-end confirmation on a real Claude Desktop (install the `.mcpb`, check the form writes env vars and the 14 tools appear) — maintainer's machine, can't be done headless.
- [ ] (optional) Build all-platform `.mcpb` files in the release pipeline and attach to the Release. Consider signing (`mcpb sign`) to drop the "unsigned" warning.

## Acceptance criteria

- Double-clicking `nodered-mcp.mcpb` installs the server in Claude Desktop and shows a form for URL / token / backup dir; after filling it, the 14 tools appear
