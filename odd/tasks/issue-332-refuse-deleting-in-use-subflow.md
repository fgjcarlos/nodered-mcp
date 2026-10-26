# Issue #332 — Refuse deletion of subflow definitions that are still in use

## Objective
Make `delete_subflow` refuse to remove a definition that the runtime still
instantiates. An instance node of type `subflow:<id>` is the only thing that
keeps a definition alive, and the runtime resolves that type at deploy time:
removing the definition anyway leaves the instance pointing at a missing type,
the runtime logs `Waiting for missing types to be registered`, and the affected
flows never deploy. The refusal must name every dependency at once.

## Problem and evidence
`DeleteSubflow` (internal/nodered/subflows.go) read `/flow/global`, dropped the
named definition and wrote the rest back. The code documented the gap in a
comment: *"We do not add a 'is anyone using this?' pre-check because the editor
does not either — the operator is expected to be aware."* The editor's behaviour
is not a safety property: the MCP tool is used by a model that does not know
what the operator had in mind.

Reproduction from the issue, against Node-RED 5.0.8: deploy the definition `sf`
and a node `type:"subflow:sf"`, then call `delete_subflow(sf)`. The tool
reported success. `/flows` kept the instance and lost the definition, and the
runtime log showed:

```
Waiting for missing types to be registered:
 - subflow:sf
```

The impact is on flow startup, not only on the editor representation.

Audit baseline: main @ 93f47b1 (audit-2026-10-19, area A05, P1).

## Scope and constraints
- Fix #332 only. No change to the node denylist, the wire validation, or the
  backup snapshot/prune safeguards.
- Reuse the existing flat-flow helpers (`FlowArray`) and the existing
  `fetchGlobalSubflows` read; no new endpoint and no new dependency.
- Both instance shapes must be covered: an instance in a flow tab and an
  instance nested inside another subflow definition.
- Cascading deletion stays out of scope: removing the instances along with the
  definition is a destructive operation the operator has to ask for explicitly.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Refuse the deletion of a subflow definition that is still in use.
  - Route: delegated direct (one non-trivial file plus regressions).
  - Risk: high — this guards a destructive write on the global config.
  - Acceptance: see issue #332 bullets; a referenced definition is not deleted
    and the runtime is left untouched; an unreferenced definition still deletes.
  - Verification: focused regression RED then GREEN, `go test ./...`,
    `go test -race ./...`, `go vet ./...`, gofmt, tool-count parity tests.
  - Runtime proof: local httptest Node-RED fixtures; no real Node-RED.
  - Rollback: revert the one issue-scoped commit.
  - Commit: the fix, its regressions, and this record.

## Delivery
Forecast: roughly 120–220 authored changed lines; advisory estimate.
Actual: 425 changed lines (196 client, 205 tests, 24 tool description and docs).
Strategy: ask-on-risk, single commit, one PR.

## Progress
Task document created before the first source write.

Root cause: `DeleteSubflow` treated a definition as a standalone record. The
runtime does not: an instance node of type `subflow:<id>` is resolved at deploy
time, so the definition and its instances are one unit.

Fix: `subflowReferences` walks the runtime for references to the definition
before the delete. Instances in flow tabs come from `GET /flows`; instances
nested inside another subflow definition come from the `nodes` array of that
definition, which the delete already reads. The two shapes are told apart by
the owner kind: an instance whose owner is a tab is reported as such, one whose
owner is another subflow is reported as nested. A definition whose `nodes`
field is unreadable is an error, not an empty result — "cannot be checked" must
not be read as "no references", or the hole reopens.

The refusal is `*SubflowInUseError`, which carries every reference at once: an
operator who has to delete one instance, retry, and find the next one has a
much worse time than one handed the full list. The check runs before the
backup and the write, so a refused delete costs one read and changes nothing.

RED observed on the three refusal regressions before the fix; GREEN after. The
positive control — a definition nothing instances — deletes in both states, so
the guard does not turn into a blanket refusal.

One existing test changed meaning: `TestSubflowCRUD_EndToEnd` deleted a
subflow while its instance was live, which is exactly the behaviour the issue
reports. It now expects the refusal, removes the instance, and deletes the
definition afterwards.

## Next step
Push the branch, open one PR linking #332, and merge once CI is green.
