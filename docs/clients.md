# MCP client integration

All examples below use the stdio transport. For HTTP, see the
"HTTP variant" section at the bottom.

`nodered-mcp init` never writes `NODERED_TOKEN` into generated snippets or
configuration files. Set the token in the client process environment, OS
keychain, or a secret manager before starting the client. `init --write` only
succeeds for clients with a supported config target; for other clients it
prints manual instructions and exits with an error so automation can detect
that nothing was applied.

## Claude Desktop

Edit `claude_desktop_config.json` — `%APPDATA%\Claude\` on Windows,
`~/Library/Application Support/Claude/` on macOS. See
[`examples/claude_desktop_config.json`](../examples/claude_desktop_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880"
      }
    }
  }
}
```

## Claude Code

```bash
claude mcp add -s user nodered \
  -e NODERED_URL=http://localhost:1880 \
  -- nodered-mcp
```

`-s user` registers the server for your whole account. Without it
`claude mcp add` defaults to `--scope local`, which binds the server
to the directory you happened to run the command in — it then fails
to appear in every other project, with no error to explain why. Check
the result with `claude mcp list`.

## Cursor

`.cursor/mcp.json` in your workspace, or `~/.cursor/mcp.json`
globally. Same shape as the Claude Desktop snippet above. See
[`examples/cursor_mcp.json`](../examples/cursor_mcp.json).

## VS Code

`.vscode/mcp.json` in your workspace. Note the root key is
`servers`, not `mcpServers`. See
[`examples/vscode_mcp.json`](../examples/vscode_mcp.json).

```json
{
  "servers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880"
      }
    }
  }
}
```

## Gemini CLI

`~/.gemini/settings.json`. Same shape as the Claude Desktop snippet.
See [`examples/gemini_settings.json`](../examples/gemini_settings.json).

## OpenCode

OpenCode uses a top-level `mcp` key (not `mcpServers`) and declares
each server as `local` (spawned command) or `remote` (HTTP endpoint).
Place the snippet in `~/.config/opencode/opencode.json` (user-global)
or `./opencode.json` (project-local). See
[`examples/opencode_config.json`](../examples/opencode_config.json).

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "nodered": {
      "type": "local",
      "command": ["nodered-mcp"],
      "enabled": true,
      "environment": {
        "NODERED_URL": "http://localhost:1880"
      }
    }
  }
}
```

For the HTTP transport variant, use `type: "remote"` with `url` and
`headers`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "nodered": {
      "type": "remote",
      "url": "http://localhost:8090/mcp",
      "enabled": true,
      "headers": {
        "Authorization": "Bearer your-token"
      }
    }
  }
}
```

Restart OpenCode after editing. All 44 tools should appear under
the `nodered` server.

## Pi (pi-mono)

Pi ships MCP support through a third-party adapter
(`pi-mcp-adapter` / `pi-mcp-extension`), not in the core. Install
both, then write the config:

```bash
npm install -g --ignore-scripts @earendil-works/pi-coding-agent
pi install mcp-adapter
```

Then write `~/.pi/agent/mcp.json` (global) or `./.pi/mcp.json`
(project-local). See
[`examples/pi_mcp_config.json`](../examples/pi_mcp_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880"
      },
      "lifecycle": "keep-alive"
    }
  }
}
```

`lifecycle: "keep-alive"` is recommended for nodered-mcp: it
reconnects automatically after a Node-RED restart, which matters
because Node-RED bounces during flow deployments. The default
`lazy` only connects on the first tool call and disconnects after
idle, which can mask connection issues.

Inside Pi, run `/reload` to pick up the config, then
`mcp({ connect: "nodered" })` to verify the connection and
`mcp({ server: "nodered" })` to list the 44 tools.

For the HTTP transport variant:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "auth": "bearer",
      "bearerToken": "your-token"
    }
  }
}
```

## HTTP variant

Start the server once, then point the client at the endpoint rather
than at a command. In clients that support `url` or `type: http`:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "headers": { "Authorization": "Bearer your-token" }
    }
  }
}
```

Drop the `headers` block only if the server is bound to loopback
and running without a token.

Restart the client after connecting. All 44 tools should appear.
