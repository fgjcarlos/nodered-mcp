# 103 — Setup docs/configs per MCP client

**Labels:** docs, adoption
**Milestone:** 2 — Reach

## Context

Only a Claude Desktop example exists (`examples/claude_desktop_config.json`). Each MCP client has its own config shape; ready-to-paste snippets remove the main adoption barrier. Docs beat code here — no installer command until copy-paste demonstrably fails users.

## Tasks

- [ ] `examples/` snippet + README section per client:
  - [ ] Claude Desktop (`claude_desktop_config.json`)
  - [ ] Claude Code (`claude mcp add ...` one-liner)
  - [ ] VS Code (`.vscode/mcp.json`)
  - [ ] Cursor (`.cursor/mcp.json`)
  - [ ] Gemini CLI (`~/.gemini/settings.json`)
- [ ] Each snippet shows stdio; one shared subsection shows the HTTP variant (after 101)
- [ ] Troubleshooting: token scopes, Node-RED adminAuth, Windows paths

## Acceptance criteria

- A user of any listed client can connect by copying one snippet and filling URL + token
