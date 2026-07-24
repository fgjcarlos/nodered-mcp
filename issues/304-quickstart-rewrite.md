# 304 — Rewrite the install/Quick Start

**Labels:** docs, dx
**Milestone:** 4 — Distribution / DX
**Status:** ✅ done

## Context

From a first-time user's view the old README read as "this is a Claude thing" and assumed the Go toolchain. Two wrong impressions to fix: (1) it's a **generic MCP server**, provider-agnostic; (2) there must be an install path that doesn't need Go.

## What was done

- [x] New intro callout: nodered-mcp is a generic MCP server, same binary for any MCP client (Claude, Cursor+GPT, Gemini CLI, VS Code, Cline…).
- [x] Stated Linux/macOS/Windows + amd64/arm64 support up front.
- [x] Four install options ordered easiest-first: A) prebuilt binary, B) Docker, C) `go install`, D) from source — each with Linux/macOS/Windows notes.
- [x] Honest status note: B and D work today; A and C need the repo published (links to 301).

## Acceptance criteria

- A non-Go user on Linux can read the README and install via Docker or a prebuilt binary without touching a Go toolchain
