# Docs: Spanish translation of docs/, beginner guide and indexes

Branch `docs/i18n-guide` from main 7144e47 (v0.10.0). Docs-only change: English stays the source of truth,
every page in `docs/` gets a Spanish twin in `docs/es/`, a language switcher, an index, and plain-language explanations.

## Specs

- S1 — Language section at the top: "seccion al principio con enlace a documentacione en otros idiomas, en principio solo en español". Every English page (README.md and each `docs/*.md`) starts with a language switcher right under its title, `**English** | [Español](<es path>)`; every Spanish page with `[English](<en path>) | **Español**`. Only English and Spanish for now.
- S2 — "La documentacion con indice": every page longer than one screen has a table of contents (`## Contents` / `## Contenido`) after the switcher and intro, linking to its `##` sections; `docs/README.md` (and `docs/es/README.md`) is the general index of all pages, each with a one-line description of who should read it.
- S3 — "expliacada para que lo entienda cualquier persona": plain language a non-developer can follow. New `docs/getting-started.md` (from zero: what MCP is, what Node-RED is, what this server does, install, connect a client, first conversation, next steps) and `docs/glossary.md` (MCP, client, server, tool, Node-RED, flow, tab, node, subflow, palette, context, admin API, token, transport, stdio, HTTP, read-only mode, denylist, backup, …). Jargon is explained or linked to the glossary on first use. Technical accuracy is kept: no behavior is invented or removed.
- S4 — User decision (2026-11-26): translate all of `docs/` into Spanish, under `docs/es/` with the same file names. `docs/audits/` is historical and stays English-only. README.es.md stays at the root.
- S5 — User decision (2026-11-26): new beginner guide + glossary + index on every page; keep existing file names so current links do not break.
- S6 — Maintenance: CONTRIBUTING.md translation policy changes from "docs/ is not translated" to "docs/ and docs/es/ change in the same commit"; a Go test fails when a `docs/*.md` page has no `docs/es/` twin or vice versa (excluding `docs/audits/`).
- S7 — Existing CI doc checks keep passing: tool counts (`internal/mcp/readme_sync_test.go`), `docs/tools.md` table rows and `###` chapter headings, `docs/clients.md` `## <client>` headings and example links (`cmd/nodered-mcp/init_pure_test.go`), the env-var table and "N environment variables" claim in `docs/configuration.md` (`internal/config/docs_sync_test.go`). Spanish pages keep code, commands, flags, env-var names and tool names untranslated.

## Tasks

| ID | Specs | Route | Commit |
|---|---|---|---|
| T1 | S1-S3, S5 | writer A: README.md, README.es.md, docs/README.md, docs/getting-started.md (new), docs/glossary.md (new) || 636b353 |
| T2 | S1-S3, S5, S7 | writer B: docs/clients.md, docs/configuration.md, docs/troubleshooting.md, docs/transports.md || 3cf0252 |
| T3 | S1-S3, S5, S7 | writer C: docs/tools.md, docs/architecture.md, docs/roadmap.md || c81d15c |
| T4 | S1, S2, S4, S7 | writer D (after T1-T2): docs/es/ README, getting-started, glossary, clients, configuration, troubleshooting, transports || 2b7963c |
| T5 | S1, S2, S4, S7 | writer E (after T3): docs/es/ tools, architecture, roadmap || b1c747f |
| T6 | S6, S7 | inline: CONTRIBUTING.md policy, parity test, link check, full go test || this commit |

## Log

- L1 (user, verbatim): "Si dale, y ademas mejora la documentacion, seccion al principio con enlace a documentacione en otros idiomas, en principio solo en español. La documentacion con indice y expliacada para que lo entienda cualquier persona"
- L2 (decision): asked scope; user chose "Todo docs/ (recomendado si lo vas a mantener)" and "Guía nueva + índice (recomendado)".
- L3 (evidence): CONTRIBUTING.md "Translation policy" currently says files under docs/ are not translated; this feature reverses it (S6).
- L4 (evidence): T1-T3 committed (English pages, 818 authored lines in 3 commits, one commit per writer unit). T2 also fixed a stale transports.md claim that OAuth was not implemented. Slice boundary: English rewrite ends at 636b353; Spanish translation (T4, T5) and the maintenance guard (T6) follow; the PR is docs-only.
- L5 (evidence): T4/T5 Spanish pages committed; 10/10 docs pages have docs/es twins. TestDocsHaveSpanishTwins RED before docs/es existed, GREEN after. Link check over README.md, README.es.md, docs/*.md and docs/es/*.md: 598 relative links (file + anchor), 0 broken (2 checker false positives on `_` in GitHub slugs). go test ./... green.
