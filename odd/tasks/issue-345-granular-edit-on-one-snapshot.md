# Issue #345 — Apply granular edits to the same revisioned snapshot that is deployed

## Objective
A granular edit (`add_node`, `update_node`, `delete_node`, `connect_nodes`,
`disable_flow`, `enable_flow`, `instantiate_subflow`) must not be able to
silently overwrite a change another editor made. The edit was built on one
read of the runtime and deployed with the revision of another, so a change
that landed in between was overwritten without a conflict.

## Problem and evidence
`editFlow` read the nested tab (`GET /flow/:id`), applied the patch to that
document, and then `deployFlowUpdate` read the whole configuration again
(`GET /flows`, API v2) for the array to deploy and for the revision to
deploy it with. Two reads, two snapshots.

**Measured against a real Node-RED** (5.0.9 under /tmp/nrprobe, admin API
on 127.0.0.1:1880), not assumed. The race was made deterministic with a
proxy between the client and the runtime that applies the other editor's
deployment immediately before answering the client's read of `/flows`:

| Probe | Result |
|---|---|
| The other editor changes `topic` of a node; the client then patches only `name` of that node — **old code** (two reads) | the deployment is **accepted (HTTP 204)** and the runtime holds the **old** `topic`: the other editor's change is **lost**, and no 409 is raised |
| The same race — **new code** (one read) | the deployment is accepted and the runtime holds the **new** `topic` and the patched `name`: the other editor's change **survives** |
| A deployment that carries a revision the runtime no longer has | **HTTP 409** `{"code":"version_mismatch"}` — the runtime's own conflict answer, which the client already refused to work around |
| `GET /flows` (v2) on a runtime with one tab, one canvas node and one configuration node | the flat array holds **the tab and both of its members** — the whole configuration, not a list of tabs |

The fourth row is what makes the fix possible: the array the deployment
carries is the same array the edit can be built on.

The old behaviour was not a silent race either way — a conflict *after* the
snapshot was already refused with the runtime's 409. What was missing was
the other half: a change that landed *before* the snapshot was not part of
the base the edit was applied to, so the edit wrote the old value back and
the deployment carried the *new* revision, which the runtime had no reason
to refuse.

## The fix
`editFlow` reads the configuration once, with the revision that goes with
it, and every part of the write comes from that one response:

- the **base of the edit** is the tab as the snapshot holds it (the flat
  array, rebuilt into the nested shape the edit functions work on);
- the **array to deploy** is that same array, with the entries of the tab
  replaced;
- the **revision to deploy with** is that same response's revision.

`deployFlowUpdate` was split into the read (`readFlowSnapshot`) and the
write (`deployFlowUpdateOn`) so the two paths share one implementation: the
whole-tab write (`update_flow`) reads and deploys in one go, and the
granular edit reads once and deploys on the snapshot it already has.

The window that is left is between the snapshot and the deployment, and it
is the runtime's window: the deployment carries the revision of the
snapshot, so a change that lands in between makes the revision stale and
the runtime answers 409. The client does not replay the edit on top of it
(fail-closed, no conflict replay) — the tool reports the conflict and the
caller reads the flow again.

**What the base of the edit looks like now.** The nested `GET /flow/:id` is
the runtime's *reconstruction* of a tab: it drops the fields the runtime
does not keep and it puts the groups first. The flat array is the stored
configuration, field for field. Editing a tab from the flat array therefore
preserves what the nested view used to lose — the tab's `env`, `info` and
unknown fields, the configuration nodes, the order of the entries — and it
preserves the wires of a `link out` node, which the nested view drops
because the runtime recomputes them.

**The link out.** The runtime owns the wires of a `link out` that carries a
`links` array: `parseConfig` replaces them with the link targets
(@node-red/runtime/lib/flows/util.js). What the file carries is a value the
runtime overwrites, not a dangling wire. That exception used to live in the
whole-configuration path only; building the base of the edit from the flat
array brings those wires into the tab write path, so the exception moved
into the shared rule engine (`validateWires`), where every path inherits
it, and the whole-configuration path no longer has to strip the wires
before validating.

**One read instead of two.** The granular edits now make one request fewer:
the nested `GET /flow/:id` is not read any more, because the snapshot
already holds the tab.

## Tests
- `internal/nodered/edit_snapshot_test.go`:
  - `TestGranularEditKeepsAnExternalChangeToAnotherProperty` — the
    regression for this issue: a change another editor made to a property
    the edit does not touch survives the edit, the deployment carries the
    revision of the snapshot, and the edit reads no second snapshot;
  - `TestGranularEditConflictAfterTheSnapshotIsNotReplayed` — a change that
    lands after the snapshot is answered with the runtime's 409, nothing is
    deployed and the edit is not replayed;
  - `TestGranularEditAcceptsALinkOutWhoseWiresTheRuntimeOwns` — a tab whose
    `link out` carries wires the runtime owns is edited without a refusal.
- The fixtures that modelled the runtime with a list of tabs and a separate
  nested document were corrected: the flat array the runtime serves holds
  the tab **and its members**.

## What the review changed
_(pending: the adversarial review of the branch)_

## Next step
Open one PR linking #345 and merge it once CI is green.
