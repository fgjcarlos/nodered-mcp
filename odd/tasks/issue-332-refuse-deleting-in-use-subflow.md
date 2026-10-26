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
  - Runtime proof: local httptest Node-RED fixtures, plus a real Node-RED 5.0.8
    driven over its admin API (see "Runtime ground truth" below).
  - Rollback: revert the one issue-scoped commit.
  - Commit: the fix, its regressions, and this record.

## Delivery
Forecast: roughly 120–220 authored changed lines; advisory estimate.
Actual: 604 changed lines across the fix, its regressions and the docs.
Strategy: ask-on-risk, single commit, one PR.

## Progress
Task document created before the first source write.

Root cause: `DeleteSubflow` treated a definition as a standalone record. The
runtime does not: an instance node of type `subflow:<id>` is resolved at deploy
time, so the definition and its instances are one unit.

Fix: `subflowReferences` walks the runtime for references to the definition
before the delete. The refusal is `*SubflowInUseError`, which carries every
reference at once: an operator who has to delete one instance, retry, and find
the next one has a much worse time than one handed the full list. The check
runs before the backup and the write, so a refused delete costs one read and
changes nothing.

RED observed on the refusal regressions before the fix; GREEN after. The
positive control — a definition nothing instances — deletes in both states, so
the guard does not turn into a blanket refusal.

One existing test changed meaning: `TestSubflowCRUD_EndToEnd` deleted a
subflow while its instance was live, which is exactly the behaviour the issue
reports. It now expects the refusal, removes the instance, and deletes the
definition afterwards.

### Runtime ground truth (Node-RED 5.0.8)
An independent adversarial pass ran the fix against a real Node-RED 5.0.8 and
the real `delete_subflow` MCP tool over stdio. The shapes the runtime actually
produces, confirmed by reading `GET /flows` and `GET /flow/global` on a live
instance:

- `GET /flows` is a **flat** array: every node of the configuration appears at
  the top level, including the nodes and configs nested inside a subflow
  definition, which carry `z` set to the owning subflow's id. A tab instance
  carries `z` set to the tab id; an instance with no `z` is a global config
  node.
- `GET /flow/global` returns the subflow definitions in their **nested** form:
  each definition carries a `nodes` collection and a `configs` collection, and
  an instance can live in either — an instance with no canvas coordinates is
  filed in `configs`.
- The runtime's own missing-type check matches `/^subflow:(.+)$/` against the
  type of every node in the flat configuration, so a definition is in use when
  *any* flat item carries that type.

Both shapes are therefore covered by two witnesses: the flat `/flows` scan,
which sees every instance, and the definition scan, which sees the nested ones
and can name their owner.

### Findings from the adversarial pass, and what changed because of them
The first implementation was rejected by that pass. The findings that changed
the code:

1. **The `/flows` scan failed open.** Decoding a flat entry into a struct of
   string fields drops the whole entry when any of them holds a non-string
   value, so one bad `id` or `z` hid a real instance and the delete went
   through — reproduced live, with the runtime logging `Waiting for missing
   types to be registered`. The scan now decodes each field on its own
   (`decodeFlowItem`) and an unreadable field is *unknown*, not absent. A
   non-string `type` cannot match the runtime's own pattern and is not an
   instance; a non-string `id` or `z` on an item whose type does match is an
   instance, and the delete is refused.
2. **The definition scan read only `nodes`.** It now reads `configs` as well.
3. **A malformed unrelated definition blocked every delete.** The error now
   names both the definition that cannot be read and the one that was asked
   for, so the operator knows which one to repair.
4. **A self-instantiating definition could never be deleted.** The flat scan
   re-reported the definition's own content, defeating the deliberate
   exclusion in the definition scan; the runtime considers such a delete safe.
   The flat scan now skips an instance whose owner is the definition being
   deleted.
5. **The same instance could be reported twice with contradictory owners.** The
   owner kind is no longer part of the dedup key, and the kind is derived the
   same way in both scans.
6. **An ownerless instance was reported as `in tab ""`.** The owner kind is
   now one of tab, subflow, global or unknown, and the message reads
   accordingly.
7. **The refusal advertised an operation that does not exist.** The message
   and the tool description now name the operations that do exist:
   `delete_node` for an instance in a flow tab, `update_subflow` for an
   instance nested in another subflow definition.
8. **The changed behaviour had no test at the layer it is surfaced.** The MCP
   layer now has a regression asserting the refusal arrives as a plain tool
   error naming the instance and its owner, and that it is not classified as a
   transport error — a write tool that fails that way is reported as having an
   indeterminate outcome, which is the wrong advice for a refusal that changed
   nothing.

## Next step
Push the branch, open one PR linking #332, and merge once CI is green.
