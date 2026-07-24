# 006 — Ignore backups/ and .atl/

**Labels:** cleanup
**Milestone:** 1 — Cleanup

## Context

`backups/` holds runtime flow snapshots written by the backup feature and is not gitignored. `.atl/` is AI-tooling cache unrelated to the Go server.

## Tasks

- [ ] Add `backups/` and `.atl/` to `.gitignore`
- [ ] Remove the existing `backups/*.json` snapshots and `.atl/` from the tree

## Acceptance criteria

- Runtime output and tooling caches never show up in `git status`
