# Glossary

**English** | [Español](es/glossary.md)

Plain-language meanings of the words used in these docs, in alphabetical
order. New here? Read [getting started](getting-started.md) first.

## Contents

[Admin API](#admin-api) · [Backup](#backup) · [Client](#client) ·
[Context](#context) · [Denylist](#denylist) · [Flow](#flow) · [MCP](#mcp) ·
[Node](#node) · [Node-RED](#node-red) · [Palette](#palette) ·
[Prompt](#prompt) · [Read-only mode](#read-only-mode) ·
[Resource](#resource) · [Server](#server) · [stdio](#stdio) ·
[Streamable HTTP](#streamable-http) · [Subflow](#subflow) · [Tab](#tab) ·
[Token](#token) · [Tool](#tool) · [Transport](#transport)

## Terms

### Admin API

The web interface that [Node-RED](#node-red) offers for managing itself:
reading and deploying [flows](#flow), installing nodes, reading settings.
`nodered-mcp` talks to Node-RED only through it. Deeper:
[architecture](architecture.md).

### Backup

A saved copy of your flow configuration. `nodered-mcp` takes one
automatically before a write that changes the flows, and you can list and
restore them with tools. A backup does not include Node-RED's saved
credentials. Deeper: [backup recovery limits](tools.md#backup-recovery-limits).

### Client

The AI app you talk to, such as Claude Desktop, Claude Code or Cursor. It
connects to a [server](#server) and uses its [tools](#tool). Deeper:
[clients](clients.md).

### Context

Memory that Node-RED keeps for values shared between [nodes](#node), at node,
[flow](#flow) or global level. The tools `get_context` and `set_context` read
and write it. Deeper: [tools](tools.md#runtime-diagnostics-recovery).

### Denylist

A list of node types that `nodered-mcp` refuses to add or deploy. By default
it blocks `exec` and `system`, which run commands on your computer. It is set
with `MCP_NODE_DENYLIST`. Deeper: [configuration](configuration.md#safety)
and [`SECURITY.md`](../SECURITY.md).

### Flow

An automation in [Node-RED](#node-red): [nodes](#node) connected by lines
(wires), drawn on a [tab](#tab). Several docs use "flow" for the tab itself,
as Node-RED's API does. Deeper: [tools](tools.md#flows).

### MCP

Model Context Protocol, a common way for AI apps to use outside programs.
An MCP [client](#client) connects to an MCP [server](#server), which offers
[tools](#tool), [resources](#resource) and [prompts](#prompt). Official site:
[modelcontextprotocol.io](https://modelcontextprotocol.io). Deeper:
[architecture](architecture.md).

### Node

One box in a [flow](#flow), doing one small job: receive a message, change
it, send it somewhere. Each node has a type, such as `inject`, `function` or
`exec`. Deeper: [tools](tools.md#flows).

### Node-RED

A free tool for building automations by connecting [nodes](#node) in a
browser editor, often used for home automation, IoT and data processing.
`nodered-mcp` lets an AI app work with it. Official site:
[nodered.org](https://nodered.org).

### Palette

The collection of node types installed in Node-RED, shown on the left side
of its editor. Tools can list, search, install, enable and disable them.
Deeper: [tools](tools.md#palette).

### Prompt

Two meanings. In everyday use, the message you type to the AI. In
[MCP](#mcp), a ready-made instruction template that a [server](#server)
offers; this one offers `explain_flow` and `generate_flow`. Deeper:
[tools](tools.md#prompts-2).

### Read-only mode

A setting (`--read-only` or `MCP_READ_ONLY=true`) where the server offers only
the tools that read, so the AI cannot change anything. Deeper:
[configuration](configuration.md) and [tools](tools.md).

### Resource

A piece of read-only data an [MCP](#mcp) [server](#server) offers under an
address, for example the current flows at `nodered://flows/current`. Deeper:
[tools](tools.md#resources-3).

### Server

A program that offers [tools](#tool) to a [client](#client). `nodered-mcp` is
an MCP server. Not to be confused with the machine that runs Node-RED.
Deeper: [architecture](architecture.md).

### stdio

The default [transport](#transport): the client starts `nodered-mcp` itself
and they exchange messages through the program's standard input and output.
No network port is opened. Deeper: [transports](transports.md#stdio-default).

### Streamable HTTP

A [transport](#transport) where `nodered-mcp` runs as a long-lived web
server (endpoint `/mcp`) that one or more clients connect to. It needs a
[token](#token) unless it listens on the local machine only. Deeper:
[transports](transports.md#http-streamable-http).

### Subflow

A group of [nodes](#node) saved as one reusable node, so you can use the same
logic in several [flows](#flow). Deeper: [tools](tools.md#flows).

### Tab

One page of the Node-RED editor, holding a [flow](#flow). Tools that work on
a whole flow take the tab's identifier. Deeper: [tools](tools.md#flows).

### Token

A long secret string used like a password. `NODERED_TOKEN` lets
`nodered-mcp` log in to a Node-RED that requires it; `MCP_HTTP_TOKEN`
protects the HTTP [transport](#transport). Never share it. Deeper:
[configuration](configuration.md).

### Tool

One action the AI can ask a [server](#server) to perform, such as
`list_flows` or `create_flow`. Each is classified read, write or action.
Deeper: [tools](tools.md).

### Transport

The way a [client](#client) and a [server](#server) exchange messages:
[stdio](#stdio) or [streamable HTTP](#streamable-http). Deeper:
[transports](transports.md).
