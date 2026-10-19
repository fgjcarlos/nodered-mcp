# Issue 324 — Preserve tab order on updates
Branch: `fix/issue-324-preserve-tab-order`; delivery: `auto-chain`; chain strategy: `feature-branch-chain`; original forecast ~350 authored lines; current ~585 before MCP mock adaptations.
Runner: Go tests and repository complexity check; Engram mirror pending (no callable memory tools).

## Specs
S1. User authorization: “Resuelve la issue abierta”. Issue #324: “fix: writes via PUT /flow/:id move the edited tab to the end of the tab order”.
S2. Expected outcome (issue verbatim): “The node's name changes and everything else stays as it was, including the position of the tab in the editor.”
S3. Preservation constraint (issue verbatim): “The existing snapshot/backup step and write guard can stay as they are.”
S4. Reproduction (issue verbatim):
1. Have at least three tabs, and note their order with `GET /flows`.
2. Call `update_node` on any node in a tab that is not the last one, changing only `name`.
3. `GET /flows` again: that tab is now last.

## Tasks
T1 | S1–S4 | delegated writer (context backstop) | done | B1 public RED/GREEN and focused/package/full suite/complexity PASS; hidden credentials retained without restoring operational fields | commit: 427d097 (snapshot preparation 9eefc23)
T2 | S1–S4 | independent verifier + parent spot check | done | Independent initial + B1-limited recheck PASS; parent public spot-check PASS; differential 16/16 cases compatible | commit: 427d097
T3 | S1–S4 | inline | done | Baseline-safe preparation and final behavior committed; slice boundaries and proof recorded | commits: 9eefc23, 427d097; final tracking record follows

## Log
L1. User request verbatim: “Resuelve la issue abierta”.
L2. Issue #324 source: https://github.com/fgjcarlos/nodered-mcp/issues/324

### `nodered-mcp version` output

```
0.7.2
```

npm package `@fgjcarlos/nodered-mcp@0.7.2` (linux-x64), stdio transport, Claude Desktop. Node-RED 5.0.7 (Home Assistant add-on), 18 tabs, ~1900 nodes.

### What did you do?

MCP tool call from Claude Desktop:

```json
{
  "tool": "update_node",
  "arguments": {
    "flow_id": "6f16bd5e4de5ae8f",
    "node_id": "98e76e143ec806a2",
    "properties": "{\"name\": \"Airco [test]\"}"
  }
}
```

Target: a `comment` node, only `name` changed. The node lives on the second-to-last of 18 tabs.

Server configuration (`claude_desktop_config.json`):

```json
"command": "node",
"args": [".../node_modules/@fgjcarlos/nodered-mcp/bin/nodered-mcp.js"],
"env": {
  "NODERED_URL": "http://<ha-host>:<port>",
  "MCP_READ_ONLY": "false",
  "NODERED_BACKUP_DIR": ".../backups"
}
```

Node-RED is reached directly over HTTP on the LAN (the add-on's direct port, not ingress).

### What did you expect?

The node's name changes and everything else stays as it was, including the position of the tab in the editor.

### What happened instead?

The name changed correctly and the pre-write backup was created, but the edited tab moved to the last position in the tab order:

- before (confirmed from the automatic backup): `… Weer, Logeerkamer, Seerr Telegram Approve`
- after (`GET /flows`): `… Weer, Seerr Telegram Approve, Logeerkamer`

**Cause.** `UpdateFlow` in `internal/nodered/flows.go` calls `PUT /flow/<id>`. In the Node-RED runtime, `updateFlow()` (`@node-red/runtime/lib/flows/index.js`) filters the tab's nodes out of the active config and appends the new ones with `newConfig.concat(nodes)`, so every tab updated this way ends up last. It is Node-RED behaviour, but every write through this server triggers it. On large installations tabs are often ordered deliberately, and users lose that order without noticing. `update_flow` reaches `UpdateFlow` directly (`tools_flows.go:192`). For `update_node` this is inferred from the observed result and its required `flow_id` argument; I did not trace it in the code. `main` (fc5ec48) still uses `PUT /flow/:id`.

**Suggested fix.** `GET /flows`, replace the target tab's nodes **in place**, and `POST /flows` the full array with `Node-RED-Deployment-Type: flows` and the current `rev`. Deployment type `flows` only restarts changed flows, so the runtime impact is the same as today and the tab order is preserved. The existing snapshot/backup step and write guard can stay as they are.

**Workaround.** After each write, drag the tab back in the editor and deploy.

### Reproduction

1. Have at least three tabs, and note their order with `GET /flows`.
2. Call `update_node` on any node in a tab that is not the last one, changing only `name`.
3. `GET /flows` again: that tab is now last.


L3. Exploration: UpdateFlow/updateFlowLocked is the shared mutation path for update_flow and editFlow (UpdateNode/AddNode/DeleteNode/ConnectNodes). Existing PUT endpoint reorders tabs. Upstream Node-RED 5.0.7 confirms filter-and-concat behavior and API v2 revision conflicts; request v2 explicitly, preserve raw flat configuration and metadata, send deployment type flows, do not replay stale revisions. Preserve validation ordering, backups and write guard. Handle subflows/global configs without loss. Implementation details are not extra product requirements.
L12. Parent spot-check: go test ./internal/nodered -run '^(TestUpdateNodePreservesTabOrder|TestUpdateFlowReplacesOperationalFields|TestSharedFlowEditsPreserveOtherEntries|TestUpdateFlowRevisionConflict)$' -count=1 and git diff --check PASS. Delegated isolated differential baseline c42831c vs candidate: identical probe binaries exit 0; comparison 16/16 PASS. All shared operations (UpdateFlow/AddNode/UpdateNode/DeleteNode/ConnectNodes/SetFlowDisabled true+false) and nine invalid/missing-target cases retain values/errors/counters. Expected differences: order/metadata/hidden credential preservation, v2 protocol and backup envelope. Exact outputs retained in /tmp/issue-324-compat.qcDXXH; no live Node-RED run.
L15. Slice B committed as 427d097 (fix(nodered): preserve tab order when updating flows), 969 authored lines, including behavior, tests and user-facing docs. Boundary: 9eefc23..427d097. Local total 975 implementation/tests/docs lines plus this feature record. Proposed future chain: preparation branch at 9eefc23 -> behavior branch at 427d097 with this final record. No remote branches/PRs created. All tracked tasks complete. Remaining limits: no live Node-RED runtime, no remote CI for this unpublished branch, native diff assessment unavailable (untracked declaration) but required independent verification completed, Engram mirror unavailable. Issue #324 remains open pending human-authorized delivery.
L14. Slice A verified in isolated baseline copy with only backup.go: go test ./internal/nodered -count=1 PASS (exit 0). Committed as 9eefc23 (refactor(nodered): share flow snapshot persistence), 6 authored lines. Slice A base c42831c; slice B base 9eefc23.
L13. Delivery slicing pass: user-selected feature-branch-chain. Slice A: backup.go six-line snapshot-persistence extraction (baseline-safe), linked S3 preparation. Slice B: coordinated protocol fix, all regressions, affected mocks, README/CHANGELOG and this log. Implementation/tests/docs are 975 authored lines total; main slice ~969 plus tracking. No further coherent all-green sub-400 split without divorcing tests from behavior. Budget is advisory: retain necessary coverage, expose oversized review workload, no size-only rewriting. Both slices remain local; no push/PR/issue closure/merge authorized.
L11. B1 corrected: public regression RED (omitted wires/repeat/once/custom resurrected), then GREEN. Full suite/complexity/diff checks PASS. Scoped independent recheck passed original omitted-wire probe, committed regression, name-only/credentials preservation and node-owned z refs. No remaining B1 blocker. Parent compatibility differential and spot-check pending; no live Node-RED runtime proof available. Updated authored count: 975 lines excluding feature doc.
L10. Independent verification found B1: blanket overlay resurrects omitted wires after removing their target node; candidate fails, baseline passes public UpdateFlow probe. One correction batch authorized within original S2/S3: respect replacement semantics for operational node properties, preserve hidden credentials/tab metadata, validate effective result. Existing pre-snapshot nested-read concurrency window reproduced on both baseline/candidate: advisory, not scope expansion. Final reverify limited to B1.
L8. T1 completed: full go test ./..., nodered package, diff check and complexity check PASS. Additional RED/GREEN for unchanged raw-entry preservation and null submission guard. Authored implementation/tests/docs: 858 lines (736+/122-), excluding feature doc. Worker found no natural all-green sub-400 split; a preparatory persistence/mock-helper slice is possible, but core behavior and integrated tests remain larger. User-selected feature-branch chain remains selected; no publication authorized.
L9. ASSESS returned independent verifier required for Risk item 1. Native diff assessment unavailable due to explicit-untracked declaration requirement; no native review requested (RDD off). Follow high-risk independent verification plan without repeating assessment.
L6. RED observed for tab-order regression, revision conflicts and invalid snapshots; focused/package tests GREEN. Full go test ./... failed in 15 MCP top-level tests with legacy mocks. git diff --check and complexity check passed. Current change: ~585 authored lines before MCP adaptations.
L7. User decisions verbatim: “Autorizar los cuatro archivos (recomendado)” (internal/mcp/setcontext_concurrency_test.go, tools_flows_test.go, tools_test.go, version_gate_enforcement_test.go) and “Cadena sobre rama de feature (recomendado)” (feature-branch-chain). Scope: adapt mocks and retain coverage, not weaken production safety. No publication authorized. Plan slice boundaries after the complete verified diff; avoid artificial code/test separation.
L5. User authorized edit-surface expansion: “Autorizar ambos archivos (recomendado)” — internal/nodered/backup.go and internal/nodered/subflows_test.go. No source edits or RED before this decision.
L4. Baseline: origin/main c42831c. Risk: item 1 (full configuration deployment), item 3 (shared mutation helper), item 4 (external revision conflicts). No push, PR publication, issue closure or merge is authorized by this work unit. Delivery scope: one coherent fix; revisit delivery strategy if actual diff exceeds forecast/budget before commit.
