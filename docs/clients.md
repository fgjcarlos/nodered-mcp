# MCP client integration

**English** | [Español](es/clients.md)

This page is for connecting an AI app (an MCP [client](glossary.md#client),
such as Claude Desktop, Claude Code or Cursor) to Node-RED through
`nodered-mcp`. Every client keeps a small configuration file that lists the
MCP servers it may start; you add one entry that says "run `nodered-mcp` and
talk to this Node-RED". You need it right after installing, or whenever you
add another app or another Node-RED instance. New to all this? Read
[Getting started](getting-started.md) first.

**Contents**

- [Several Node-RED instances](#several-node-red-instances)
- [Previewing a merge](#previewing-a-merge)
- [Claude Desktop](#claude-desktop)
- [Claude Code](#claude-code)
- [Cursor](#cursor)
- [VS Code](#vs-code)
- [Gemini CLI](#gemini-cli)
- [OpenCode](#opencode)
- [Pi](#pi)
- [HTTP variant](#http-variant)

**How to use this page**

1. Easiest path: run `nodered-mcp init`. It asks a few questions (which
   client, the Node-RED URL) and prints the snippet for your client. With
   `--write` it also edits the client's configuration file for you, when
   that client has a supported file.
2. Or find your client in the list below and copy its snippet by hand.
3. Restart the client so it reads the new configuration.

All examples below use the [stdio](glossary.md#stdio) transport: the client
starts `nodered-mcp` itself. For [HTTP](glossary.md#streamable-http), see the
"HTTP variant" section at the bottom.

`nodered-mcp init` never writes `NODERED_TOKEN` into generated snippets or
configuration files. The [token](glossary.md#token) is the password-like
value Node-RED gives you to prove who you are. Set it in the client process
environment, OS keychain, or a secret manager before starting the client.
`init --write` only succeeds for clients with a supported config target; for
other clients it prints manual instructions and exits with an error so
automation can detect that nothing was applied.

Use `nodered-mcp init --name nodered-lab` to register a second Node-RED
instance under a different server name. Names accept ASCII letters, digits,
underscores, and hyphens, but cannot start with a hyphen. Enter the instance's
URL and a distinct absolute backup directory when prompted.

Both `--write` and `--dry-run` preserve other server entries. If a name passed
with `--name` already exists, `--write` asks for confirmation, and `--dry-run`
shows the prospective merge without asking. Omitting `--name` retains the
previous behavior: `--write` updates the `nodered` entry without prompting.

## Several Node-RED instances

In practice: if you have a test Node-RED on port `1880` and a production one
on port `1881`, you register two servers (for example `nodered-1880` and
`nodered-1881`) so you can tell the assistant which one to work on.

Run one `nodered-mcp` server per Node-RED instance. Give each server a
unique name, its own `NODERED_URL` and credentials, and a separate,
**absolute** `NODERED_BACKUP_DIR` that the server can write to.

> [!WARNING]
> Don't share a backup directory. The default, `backups`, is relative to
> the MCP process's working directory. If two servers resolve to the same
> directory, `restore_backup` with `latest` can deploy another instance's
> flows, and `diff_flows` with `latest` can compare against the wrong snapshot.

The examples below connect to Node-RED on ports `1880` and `1881`.
Replace `/home/you` with your actual home directory. Launch Claude Code
with `NODERED_TOKEN_1880` and `NODERED_TOKEN_1881` set in its environment
to the respective instances' bearer tokens. If an instance doesn't
require authentication, omit its `NODERED_TOKEN` setting. For other
authentication options, see [Connecting to Node-RED](configuration.md#connecting-to-node-red).

**JSON (Claude Code `.mcp.json`):** Add these entries under `mcpServers`.
Claude Code expands the token references at launch; don't put token
values in this file.

```json
{
  "mcpServers": {
    "nodered-1880": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_BACKUP_DIR": "/home/you/.local/share/nodered-mcp/1880/backups",
        "NODERED_TOKEN": "${NODERED_TOKEN_1880}"
      }
    },
    "nodered-1881": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1881",
        "NODERED_BACKUP_DIR": "/home/you/.local/share/nodered-mcp/1881/backups",
        "NODERED_TOKEN": "${NODERED_TOKEN_1881}"
      }
    }
  }
}
```

**Claude Code CLI:** Alternatively, run these commands from the project
directory. Keep the single quotes so the shell passes the token references
literally instead of putting token values in the saved configuration.

```bash
claude mcp add --scope project \
  --env NODERED_URL=http://localhost:1880 \
  --env NODERED_BACKUP_DIR=/home/you/.local/share/nodered-mcp/1880/backups \
  --env 'NODERED_TOKEN=${NODERED_TOKEN_1880}' \
  --transport stdio nodered-1880 -- nodered-mcp

claude mcp add --scope project \
  --env NODERED_URL=http://localhost:1881 \
  --env NODERED_BACKUP_DIR=/home/you/.local/share/nodered-mcp/1881/backups \
  --env 'NODERED_TOKEN=${NODERED_TOKEN_1881}' \
  --transport stdio nodered-1881 -- nodered-mcp
```

Run `claude mcp list` to check that both names are registered. Before
restoring a snapshot, make sure you've selected the intended MCP server.

## Previewing a merge

In practice: run `nodered-mcp init --dry-run` to see what would be written
before you let `--write` touch your configuration file.

`nodered-mcp init --dry-run` prints the configuration that `--write`
would merge, without touching the filesystem. It creates no file, no
parent directory, no `.bak` backup and no temporary file, and it changes
no permissions. Useful when your config already holds other MCP servers
and you want to verify the real merged result before applying it — the
snippet printed by a plain `init` is a freshly generated example, not the
merge.

The preview matches the file `--write` would produce, with one deliberate
exception: where the preview shows `REDACTED`, the file keeps the real
value. Everything else — keys, structure, the selected entry, your other
servers — is identical.

Secrets are redacted in the preview: the generated server entry never
carries `NODERED_TOKEN`, and secret-bearing values belonging to other
servers already in your file are masked too. Redaction matches key names
(`NODERED_TOKEN`, `TOKEN`, `API_KEY`, `PASSWORD`, `SECRET`) exactly, so a
secret stored under an unexpected key, inside an `args` array, embedded
in a URL, or held as a non-string value is **not** masked. Treat the
preview output as sensitive if your config stores tokens unusually.

The same limitation as `--write` applies: for clients with no safe
config target (Claude Code, VS Code, OpenCode, Pi) the preview prints
the same manual snippet a plain `init` prints for your answers (still
without `NODERED_TOKEN`) and exits with an error, so automation can detect
that nothing was merged. `--dry-run` and `--write` together are
rejected as contradictory.

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

## Pi

Pi (pi-mono) ships MCP support through a third-party adapter
(`pi-mcp-adapter` / `pi-mcp-extension`), not in the core. Install
both, then write the config:

```bash
npm install -g --ignore-scripts @earendil-works/pi-coding-agent
pi install mcp-adapter
```

Then write `~/.pi/agent/mcp.json` (global) or `./.pi/mcp.json`
(project-local). When `PI_CODING_AGENT_DIR` is set, Pi reads the global
file from `$PI_CODING_AGENT_DIR/mcp.json` instead, and `nodered-mcp init`
names that path. See
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

Use this when one `nodered-mcp` process runs on its own (see
[Transports](transports.md)) and the client connects to it over the network
instead of starting it. Start the server once, then point the client at the
endpoint rather than at a command. In clients that support `url` or
`type: http`:

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

Replace `your-token` with the value of `MCP_HTTP_TOKEN` set on the server.
Drop the `headers` block only if the server is bound to loopback (reachable
only from this computer) and running without a token.

Restart the client after connecting. All 44 tools should appear.
