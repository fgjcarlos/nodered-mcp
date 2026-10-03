# Issue #311 — Enforce node denylist when restoring flow backups

## Objective
Make `restore_backup` apply the same `MCP_NODE_DENYLIST` policy every other
write tool applies, so a backup saved before a policy change cannot restore a
now-prohibited node type. Reject before any deployment, naming the type.

## Problem and evidence
`handleRestoreBackup` (internal/mcp/tools_backups.go:95) reads the backup and
calls `nrClient.RestoreFlows` directly. It never calls
`findDeniedNodeInFlowsArray` / `findDeniedNodeInFlow`, unlike
`handleSetFlows` (internal/mcp/tools_runtime.go:573),
`handleCreateFlow` / `handleUpdateFlow` (internal/mcp/tools_flows.go:150,184)
and `handleAddNode` (internal/mcp/tools_flows.go:213). The client
(`nodered.RestoreFlows`, internal/nodered/flows.go:209) has no policy access,
so the guard belongs in the MCP handler layer where the denylist lives.

Confirmed by code tracing, not a production exploit. Follow-up to closed #81.
Audit baseline: origin/main 58cd699 (#310 landed via PR #317).

## Scope and constraints
- Fix #311 only. No changes to the client, the wire validation, or the
  backup snapshot/prune safeguards.
- Reuse the existing shared walkers — no new denylist helper.
- Both backup shapes must be covered: a bare array and the
  `{"flows":[…]}` API-version envelope `extractFlowArray` accepts.
- No new dependency. No push, PR or issue closure without authorization.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Reject denylisted node types in restored backups before any write.
  - Route: delegated direct (two non-trivial files: handler + regression).
  - Risk: high — this is a security control on a destructive full-deploy path.
  - Acceptance: see issue #311 bullets; zero deployment requests for a
    rejected backup; allowed backup still restores.
  - Verification: focused regression RED then GREEN, `go test ./...`,
    `go test -race ./...`, `go vet ./...`, gofmt, `git diff --check`,
    independent read-only verifier plus a parent focused spot check.
  - Runtime proof: local httptest Node-RED fixtures; no real Node-RED.
  - Rollback: revert the one issue-scoped commit.
  - Commit: b8deb2f (guard, shared extractor export, regressions).

## Delivery
Forecast: roughly 120–220 authored changed lines; advisory estimate.
Actual: 190 changed lines (23 handler, 139 tests, 28 rename/export).
Strategy: ask-on-risk, single commit, one PR. Push and PR remain the
maintainer's decision.

## Progress
Task document created before the first source write. Engram mirror pending:
this runtime exposes no Engram tools.

Root cause: `handleRestoreBackup` read the backup and called
`RestoreFlows` with no policy check, while create_flow / update_flow /
add_node / set_flows all call the shared walkers. The client has no
denylist, so the guard belongs in the handler.

Fix: reuse `findDeniedNodeInFlowsArray` over `nodered.FlowArray` — the
extractor `RestoreFlows` itself deploys with, exported rather than
duplicated, so no accepted shape can slip past the check. The guard
sits after the local file read and before the lock, the snapshot and
POST /flows, so a rejected backup costs zero runtime requests.

RED observed on all four shapes (flat array, `{"flows":[…]}`, subflow
definition, nested tab) before the fix; GREEN after. No new dependency.

## Next step
Await independent verification, then push the branch and open one PR
linking #311 (both remain the maintainer's decision).

