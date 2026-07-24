# 306 — `init` command: interactive, universal config generator

**Labels:** cli, dx
**Milestone:** 4 — Distribution / DX

## Context

Editing MCP client JSON by hand is the #1 source of setup failures — a stale binary path already caused a `spawn ENOENT` / "Server disconnected" for the maintainer. `nodered-mcp init` removes the hand-editing: it asks for the few values that vary and prints a ready-to-paste snippet with the binary path filled in automatically.

Universal by design: works with ANY MCP client, including ones we don't special-case.

## Behavior

1. **Auto-detect the binary path** via `os.Executable()` — the snippet always points at the real binary (kills the ENOENT class of bug).
2. **Detect installed clients** by probing known config paths (`os.UserConfigDir()` + `os.UserHomeDir()`), so we only offer what's present. `--all` shows every client.
3. **Prompt** (prompts → stderr, snippet → stdout so it can be piped) with defaults:
   - Node-RED URL (`http://localhost:1880`)
   - token (optional)
   - backup dir (`backups`)
4. **Print** the client-specific snippet + the exact file to paste it into. VS Code uses `servers`, the rest use `mcpServers`; Claude Code prints a `claude mcp add …` one-liner.

## Detection table

| Client | Probe |
|---|---|
| Claude Desktop | `<UserConfigDir>/Claude/claude_desktop_config.json` |
| Claude Code | `<Home>/.claude.json` |
| Cursor | `<Home>/.cursor` |
| VS Code | `<UserConfigDir>/Code/User` |
| Gemini CLI | `<Home>/.gemini` |

## Tasks

- [ ] `init` subcommand in `cmd/nodered-mcp` (stdlib only: `flag`, `bufio`, `encoding/json`)
- [ ] Detection + `--all` override
- [ ] Snippet rendering per client (deterministic JSON)
- [ ] Test for the rendering (stable output)

## `--write` (added)

- `nodered-mcp init --write` merges the `nodered` server directly into the client's config file for clients with an unambiguous target: Claude Desktop, Cursor, Gemini CLI. Safe merge: preserves every other key, refuses to overwrite a file that isn't valid JSON, and backs up the previous file to `.bak`.
- VS Code (workspace-scoped) and Claude Code (`claude mcp add`) fall back to printing — auto-writing a guessed location would be worse than a copy-paste.
- Tests cover the merge (preserves other keys + backup), the invalid-JSON refusal, and missing-file creation.

## Acceptance criteria

- `nodered-mcp init` prints a valid snippet whose `command` is the absolute path of the running binary
- `nodered-mcp init --write` for Claude Desktop adds the server without disturbing existing entries, leaving a `.bak`
