# nodered-mcp documentation

This directory holds the long-form documentation for `nodered-mcp`,
split by responsibility. The top-level [`README.md`](../README.md) is
the entry point; everything below extends from there.

| File | Responsibility |
|---|---|
| [`architecture.md`](architecture.md) | Source tree, dependencies, design decisions, JSON-opaque flow model, backup-before-write guardrail |
| [`tools.md`](tools.md) | Catalog of the 44 MCP tools (read / write / action classification) |
| [`configuration.md`](configuration.md) | Environment variables, command-line flags, defaults |
| [`transports.md`](transports.md) | stdio and streamable HTTP transports, bearer auth, OAuth 2.1 |
| [`clients.md`](clients.md) | Per-MCP-client configuration snippets (Claude, Cursor, VS Code, Gemini CLI, OpenCode, Pi) |
| [`troubleshooting.md`](troubleshooting.md) | Common failure modes and how to recover |
| [`roadmap.md`](roadmap.md) | Open work, accepted risks, planned versions |
| [`audits/2026-07-28-v0.5.12.md`](audits/2026-07-28-v0.5.12.md) | Audit report for v0.5.12 (historical) |

The source of truth for tool counts is
`internal/mcp/tools_test.go` (`totalTools`, `readOnlyTools`). The
counts in this documentation are kept in sync with those constants.