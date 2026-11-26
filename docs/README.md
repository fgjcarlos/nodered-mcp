# nodered-mcp documentation

**English** | [Español](es/README.md)

This is the index of every documentation page for `nodered-mcp`. The
top-level [`README.md`](../README.md) is the short entry point: what the
project is, how to install it, and the safety basics. Pages below go deeper.

## Contents

- [Start here](#start-here)
- [Guides](#guides)
- [Reference](#reference)
- [Historical](#historical)

## Start here

| Page | Who should read it |
|---|---|
| [`getting-started.md`](getting-started.md) | Anyone new to MCP or Node-RED. Answers: what is this, what do I need, how do I install it and talk to my flows for the first time? |
| [`glossary.md`](glossary.md) | Anyone who meets an unfamiliar word in these docs. Answers: what does "flow", "tool", "transport" or "token" mean here? |

## Guides

| Page | Who should read it |
|---|---|
| [`clients.md`](clients.md) | Anyone connecting an AI app (Claude, Cursor, VS Code, Gemini CLI, OpenCode, Pi) to Node-RED. Answers: what do I paste into my client's config? |
| [`configuration.md`](configuration.md) | Anyone tuning the server. Answers: which environment variables and command-line flags exist, and what are their defaults? |
| [`troubleshooting.md`](troubleshooting.md) | Anyone whose setup does not work. Answers: why do the tools not appear, or why do I get a 401, and how do I recover? |
| [`transports.md`](transports.md) | Anyone running the server for more than one client or over the network. Answers: how do stdio and streamable HTTP differ, and how do I protect the HTTP endpoint (bearer token, OAuth 2.1)? |

## Reference

| Page | Who should read it |
|---|---|
| [`tools.md`](tools.md) | Anyone who wants to know exactly what the AI can do. Answers: which of the 44 MCP tools exist and which are read, write or action? |
| [`architecture.md`](architecture.md) | Contributors and reviewers. Answers: how is the code organised, and how do the backup-before-write and node-type guardrails work? |
| [`roadmap.md`](roadmap.md) | Anyone planning around the project. Answers: what is open, which risks are accepted, and what is planned? |

## Historical

[`audits/`](audits/) holds past audit reports (for example
[`audits/2026-07-28-v0.5.12.md`](audits/2026-07-28-v0.5.12.md)). They are
a record of what was true at the time, are not updated, and are English only.

The source of truth for tool counts is
`internal/mcp/tools_test.go` (`totalTools`, `readOnlyTools`). The
counts in this documentation are kept in sync with those constants.
