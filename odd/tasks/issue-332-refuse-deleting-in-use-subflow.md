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

- Tabs and subflow definitions live in **separate id maps** inside the runtime
  (`flow.flows` and `flow.subflows`), so the same id can name both, and the
  owner of a node is resolved **tab-first** (`flow.flows[n.z] ||
  flow.subflows[n.z]`).
- The runtime coerces the type with `String()` before matching it. The
  coercion matters: `String(["subflow:x"])` is `"subflow:x"`, so an
  **array-typed** node is a real reference to a subflow.
- The member list in `/flow/global` is the membership the runtime itself
  resolved, which is what makes it the right list to answer "is this node
  inside this definition".

Both instance shapes are covered by two witnesses: the flat `/flows` scan,
which sees every node, and the definition scan, which knows which nodes belong
to which definition. Neither is trusted on its own.

### Findings from the adversarial pass, and what changed because of them
The first implementation was rejected by that pass. The findings that changed
the code:

1. **The `/flows` scan failed open.** Decoding a flat entry into a struct of
   string fields drops the whole entry when any of them holds a non-string
   value, so one bad `id` or `z` hid a real instance and the delete went
   through — reproduced live, with the runtime logging `Waiting for missing
   types to be registered`. The scan now decodes each field on its own
   (`decodeFlowItem`) and an unreadable field is *unknown*, not absent. A
   non-string `id` or `z` on an item whose type does match is an instance,
   and the delete is refused. The type is compared the way the runtime
   compares it — coerced with `String()` — because an array-typed node is a
   real reference (see the second round below).
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

### Second round: findings against the first round of fixes
The first round of fixes was reviewed again and rejected: it had closed the
original findings but introduced two new ones, both reproduced live.

9. **The self-instance exclusion was too broad.** It skipped any instance whose
   owner id equalled the definition's id. Tabs and subflow definitions live in
   separate id maps and the owner resolves tab-first, so an instance whose
   owner is that id may belong to a *tab* that happens to share the id — and
   skipping it dropped a real reference. The exclusion is now by node id,
   using the member list of the definition being deleted as read from
   `/flow/global`, which is the membership the runtime itself resolved. The
   owner kind is resolved tab-first now, instead of depending on the order of
   the flat array.
10. **"A non-string type cannot be an instance" was wrong.** The runtime
    matches with `RegExp.prototype.exec`, which coerces its argument with
    `String()`, and `String(["subflow:x"])` is `"subflow:x"`. An array-typed
    node is a real reference, in a tab and nested in a definition alike. The
    guard now coerces the type the way JavaScript does (`jsTypeString`) and
    compares the coerced value.
11. **A `/flows` body this client cannot read still failed open.** The
    reference scan now extracts the flow array strictly (`subflowFlowItems`)
    and refuses the delete when the body carries no readable flow list: an
    unreadable configuration is not a configuration without instances.
12. **The wording of an unrecognised owner was ungrammatical**, and an
    instance with an unreadable id was reported as an empty name. Both read
    properly now.

### Third round: the number rendering
The second round was reviewed once more and rejected for one item: the
JavaScript number emulation.

13. **The number rendering was not JavaScript's.** `jsNumber` used Go's own
    formatting, which says "1e-07" where JavaScript says "1e-7", and "0" for
    negative zero where JavaScript also says "0" but Go's shortest form does
    not. A number can reach the comparison inside an array type, and the
    array joins with commas, so the id has to carry one for the difference
    to matter — a reachable shape through `create_subflow` (caller-chosen id)
    and `set_flows`, and reproduced live. `jsNumber` now implements the
    ECMAScript `Number::toString` algorithm: the shortest round-tripping
    digits, rendered as a plain decimal or in exponential form depending on
    where the decimal point falls.

    The rendering was then checked differentially against node — the runtime
    is V8 — over 3963 JSON-representable numbers and 4000 random JSON
    structures: every one agrees with `String()`. The only difference found
    was `Infinity`, which JSON cannot represent and a flow file therefore
    cannot carry. The number table is pinned in
    `TestJSNumberMatchesJavaScript`; the shape that motivated the fix is
    pinned in `TestDeleteSubflow_RefusesWhenInstanceTypeArrayHoldsANumber`.

The two rounds together are the reason the record says "runtime proof: local
httptest fixtures plus a real Node-RED 5.0.8": every shape the runtime can
produce was exercised against a real runtime, not reasoned about from the data
model.
## Next step
Push the branch, open one PR linking #332, and merge once CI is green.
