# Getting started

**English** | [Español](es/getting-started.md)

This page is for you if you have never used MCP or Node-RED. It takes you
from nothing to your first conversation with your own flows. Any word you
do not know is explained in the [glossary](glossary.md).

## Contents

- [What this is](#what-this-is)
- [What you need](#what-you-need)
- [Install](#install)
- [Connect your client](#connect-your-client)
- [A token, if Node-RED asks for a login](#a-token-if-node-red-asks-for-a-login)
- [Your first conversation](#your-first-conversation)
- [Staying safe](#staying-safe)
- [Next steps](#next-steps)

## What this is

[Node-RED](glossary.md#node-red) is a tool where you build automations by
drawing boxes and connecting them with lines; each automation is called a
[flow](glossary.md#flow). [MCP](glossary.md#mcp) is a common plug that lets
AI apps (the [client](glossary.md#client)) use other programs.
`nodered-mcp` is the adapter in between: think of it as a translator at a
reception desk. You tell the AI what you want in plain words, the translator
turns it into the exact requests Node-RED understands, and brings the answer
back. The AI can then read, explain and, if you allow it, change your flows.

## What you need

- A **running Node-RED**. By default `nodered-mcp` looks for it at
  `http://localhost:1880`.
- An **MCP client**: an AI app that speaks MCP, such as Claude Desktop,
  Claude Code or Cursor. [`clients.md`](clients.md) lists the supported ones.
- **Node.js 18 or newer**, only if you install with npm (the recommended way).

## Install

The recommended way is npm:

```bash
npm install -g @fgjcarlos/nodered-mcp
```

Other ways to install:

- **Docker**: see the [Docker section of the README](../README.md#docker).
- **`go install`**: see the [Install section of the README](../README.md#install).

## Connect your client

`nodered-mcp init` asks a few questions and then produces the settings your
client needs. Run it in a terminal:

```bash
nodered-mcp init --write
```

It asks for:

1. **Node-RED URL** (press Enter to keep `http://localhost:1880`).
2. **Node-RED token** (optional; press Enter to skip, see
   [the next section](#a-token-if-node-red-asks-for-a-login)).
3. **Backup directory** (press Enter to keep `backups`).

If it finds more than one installed client, it asks which one to set up. If
it finds none, it stops; rerun it with `--all`.
Useful options:

| Option | What it does |
|---|---|
| `--write` | Saves the settings into the client's config file. The previous file is kept as `.bak`. Works for Claude Desktop, Cursor and Gemini CLI. For the other clients it prints what to paste or run and reports that nothing was written. |
| `--dry-run` | Previews the `--write` merge without changing any file. With `--all`, prints a manual snippet and succeeds if no preview is available for the client. Cannot be combined with `--write`. |
| `--all` | Lists every known client, not just the ones it detected. |
| `--name <server>` | Uses a different name for the entry (default `nodered`). Useful with several Node-RED instances. |

Without `--write`, `init` only prints the settings. For Claude Code it
prints a `claude mcp add …` command for you to run. Details for each client,
including manual setup, are in [`clients.md`](clients.md).

When it finishes, **restart your client**. The Node-RED
[tools](glossary.md#tool) then appear under the server named `nodered`.

## A token, if Node-RED asks for a login

Skip this if you open the Node-RED editor without logging in. If your
Node-RED has admin authentication (`adminAuth`) turned on, `nodered-mcp` needs a
[token](glossary.md#token) with write permission on flows, or Node-RED will
answer with a 401 or 403 error.

For safety, `init` never saves your token into a config file. Make it
available as the `NODERED_TOKEN` environment variable (or in your OS keychain
or secret manager) before your client starts. Treat the token like a
password. More in [`configuration.md`](configuration.md) and
[`troubleshooting.md`](troubleshooting.md).

## Your first conversation

Open your client and try these three prompts. The exact wording does not
matter; plain language works.

1. **"List my flows."** The AI calls the `list_flows` tool and answers with
   the [tabs](glossary.md#tab) in your Node-RED. This only reads; nothing
   changes.
2. **"Explain what the flow called Heating does."** The AI reads that flow
   (`get_flow`; the server also offers a ready-made `explain_flow`
   [prompt](glossary.md#prompt)) and describes its triggers and what it
   talks to. Again, read only.
3. **"Create a flow that logs a message every 5 minutes."** The AI builds the
   flow (the server offers a `generate_flow` prompt for this) and adds it
   with `create_flow`. This *changes* Node-RED, so a
   [backup](glossary.md#backup) of your flows is saved first. Many clients
   also ask you to approve each tool call; read it before you accept.

Open the Node-RED editor afterwards to see the result.

## Staying safe

Giving an AI the power to edit an automation system deserves care. Built in:

- **Read-only mode.** Start the server with `--read-only`, or with the
  environment variable `MCP_READ_ONLY=true`, and only the 21 read tools are
  offered; every tool that changes something is hidden, including
  `inject_node`. A good first setting while you learn. See
  [`configuration.md`](configuration.md).
- **Automatic backups.** Before a write that changes the flow configuration,
  a snapshot is saved (in `backups` by default, 50 kept). If the snapshot
  cannot be saved, the write does not happen. Snapshots do not include
  Node-RED's credentials, so keep your own backup too; see
  [backup recovery limits](tools.md#backup-recovery-limits).
- **Denylist.** Nodes of the types `exec` and `system`, which run commands on
  your computer, are refused by default (`MCP_NODE_DENYLIST`). See
  [`SECURITY.md`](../SECURITY.md) before changing it.

## Next steps

- Browse the [glossary](glossary.md) for any word you did not know.
- See every tool the AI can use in [`tools.md`](tools.md).
- Tune the server in [`configuration.md`](configuration.md).
- Something not working? [`troubleshooting.md`](troubleshooting.md).
- Running it for several people or over the network:
  [`transports.md`](transports.md).
- The whole documentation: [`README.md`](README.md).
