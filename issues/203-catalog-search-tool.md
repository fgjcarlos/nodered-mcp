# 203 — Tool: search the Node-RED catalog

**Labels:** feature, tools
**Milestone:** 3 — More tools
**Status:** ✅ done — `search_nodes` tool, see `internal/nodered/settings.go::SearchNodes`.

## Context

Closes the loop with 201: the agent can find a module before installing it. Source: npm registry search scoped to the `node-red` keyword (`https://registry.npmjs.org/-/v1/search?text=keywords:node-red+<query>`), which backs the public flow library.

The registry URL is overridable via `Options.SearchBaseURL` for private mirrors (Verdaccio, GitHub Packages, …).

## What was done

- [x] Tool `search_nodes` — args: `query` (required), `limit` (optional, 1-50, default 10)
- [x] Returns name, version, description, date, link and publisher per hit
- [x] Filters registry hits to packages whose name starts with `node-red` (the registry search is fuzzy and can match unrelated packages)
- [x] Tests cover empty query, response parsing, limit clamping

