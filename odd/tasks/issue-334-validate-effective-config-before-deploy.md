# Issue #334 — Validate the effective configuration before a full or subflow deployment

## Objective
A deployment must not install a configuration whose references do not resolve.
`set_flows` (a full deployment) and `restore_backup` deployed whatever they
were given, and the subflow writes did not look at the topology of the
definition at all. The rules the tab write path already applies have to be
shared, without refusing the topologies the runtime owns: link nodes and
subflow instances. A restore of a configuration that no longer validates needs
an explicit, documented recovery path.

## Problem and evidence
The audit fixture received a deployment with `wires:[["missing"]]` and the
client returned success. That is the whole defect: the write path had no
guard at all on the paths that install a whole configuration.

**Measured against a real Node-RED 5.0.9** (installed under /tmp, admin API
on 127.0.0.1:1880), not assumed:

| Probe | Request | Result |
|---|---|---|
| Full deploy with a dangling wire | `POST /flows` (full) with `wires:[["missing"]]` | **HTTP 204**, the wire is stored as it was, the client reported success |
| Full deploy with a subflow instance and no definition | `POST /flows` with a node of type `subflow:missing-id` and no definition | **HTTP 204**, and then the runtime logs `Waiting for missing types to be registered: - subflow:missing-id` and **the flows stay stopped** |
| Full deploy with an unknown node type | same, with type `totally-not-a-real-type` | HTTP 204, same "waiting for missing types" outcome |
| Full deploy with a subflow definition and an instance of it | `POST /flows` with the definition, its inner node and the instance | HTTP 204; `GET /flows` returns **the definition and its inner node in the flat array**, and `GET /flow/global` returns the same definition with its nodes nested |
| A node whose `z` names no tab | `POST /flows` with `z:"nowhere"` | HTTP 204, the flows start, and the node is stored with that `z` — the runtime invents a tab for it (@node-red/runtime/lib/flows/util.js, parseConfig) and the node runs outside the flow that was deployed |
| Two entries with the same id | `POST /flows` with two nodes called `n1` | HTTP 204, the flows start, and the runtime keys every node by its id, so the last one wins and the earlier one is gone |

The third row is the one that matters most: a deploy the client called a
success left the runtime **stopped**, waiting for a node type that would
never be registered.

**The runtime's own rules, from the source** (@node-red/runtime/lib/flows/
util.js, parseConfig), which the guard mirrors:

- a node belongs to the tab or the subflow its `z` names, and a node with
  canvas coordinates is a canvas node while a node without them is a
  configuration node;
- a node whose `z` names nothing is filed in a tab the runtime invents;
- an instance of a subflow whose definition is missing is a *missing type*,
  and a runtime with a missing type refuses to start the flows;
- **the wires of a `link out` node are recomputed** from the `links` arrays
  (`n.wires = [targets]`), so a wire of a link out that points nowhere is a
  value the runtime overwrites — not a dangling wire.

The last point is why the guard leaves link nodes alone: a `link in` may
point at a `link out` in another tab (that is what a link is for), and a
`link out` with a `links` array has its wires rewritten by the runtime. The
one exception is a `link out` **without** a `links` field: the runtime keeps
its wires then, so they are checked like any other node's.

## Scope and constraints
- The guard is **local**: nothing is sent to the runtime and no backup is
  taken when a configuration is refused.
- The rules are shared with the tab write path, not re-implemented: the
  per-document rules are the ones `ValidateFlow` already applies, and the
  subflow writes now call the same guard functions the tab path calls.
- A full deployment replaces the whole configuration, so the subflow
  definitions in it are part of what is validated. An instance of a subflow
  whose definition is not in the configuration is refused: the runtime would
  install a node type it cannot resolve and stop.
- The recovery path is explicit. `restore_backup` refuses a backup that no
  longer validates and `force=true` installs it anyway; the result says it
  was installed unvalidated. `set_flows` has no such switch: a configuration
  the caller authors is the caller's to fix.
- The rules the runtime owns are not re-litigated: link wires and
  cross-tab link references are legal and stay legal.

## Work unit
- `internal/nodered/validate_deploy.go` (new): `ValidateEffectiveConfig`
  and the rules — the dangling wire, the orphan node, the duplicate id, the
  subflow instance without a definition. The per-owner rules are the ones
  `ValidateFlow` applies, applied to a document rebuilt per owner (a tab or
  a subflow definition), so the tab path and the deployment path judge a
  wire by the same rule.
- `internal/nodered/validate.go`: `FlowGuardError`, a typed refusal that
  keeps the message the write path has always produced and says that the
  runtime was never contacted.
- `internal/nodered/flows.go`: `validateFlowWires` and `validateZRefsInFlow`
  return that typed error; `RestoreFlows` validates the backup and
  `RestoreFlowsUnchecked` is the documented recovery path; the shared body
  moved into `restoreFlowsLocked`.
- `internal/nodered/settings.go`: `SetFlows` validates the array it is
  about to deploy, before the snapshot and before the runtime is contacted.
- `internal/nodered/subflows.go`: `CreateSubflow` and `UpdateSubflow` apply
  the guard the tab path applies — the wires rule and the owner rule — to
  the definition they are about to write.
- `internal/mcp/config_validation.go` (new): the two renderings. A refusal
  by the effective-configuration guard lists the problems with their kind;
  a refusal by the per-document guard says the runtime was not contacted.
- `internal/mcp/tools_backups.go`: `restore_backup` takes `force`.
- `internal/mcp/tools.go`, `docs/tools.md`: the descriptions advertise the
  guard and the recovery path, so a caller knows both before it tries.

## Delivery
- Tests: 17 unit tests for the rules (internal/nodered/validate_deploy_test.go),
  7 write-path tests (internal/nodered/validate_deploy_write_test.go) and
  5 tool-boundary tests (internal/mcp/config_validation_test.go).
- The complexity gate now covers the new code: floors for the seven new
  named functions are added to scripts/complexity-baseline.txt. No existing
  floor moves, and the PR adds no function above the threshold — the tree
  has the same 25 functions over 15 before and after (the gate only checks
  the functions named in the baseline, which is a known narrowness of it).

## Progress
**RED**, re-derived against the tree before the fix (`main` @ 5c1791b, in a
separate checkout) rather than against a half-reverted copy: all three paths
deployed the broken configuration.

```
--- FAIL: TestSetFlows_DanglingWireIsRefused       RED: expected SetFlows to refuse a dangling wire, got nil
--- FAIL: TestRestoreFlows_DanglingWireIsRefused   RED: expected RestoreFlows to refuse a dangling wire, got nil
--- FAIL: TestCreateSubflow_DanglingWireIsRefused  RED: expected CreateSubflow to refuse a dangling wire, got nil
```

**GREEN** on the branch: the same three tests pass, plus 26 more.

The rules, and what each one is for:

- **A wire that names no node in the same owner.** The runtime resolves a
  wire target inside its owner, so a wire across two tabs is dead. The tab
  path has always refused this; the deployment path now refuses it too.
- **A node whose `z` names no tab and no subflow.** The runtime invents a
  tab for it (parseConfig), so the node runs outside the flow that was
  deployed — and the editor shows it nowhere.
- **Two entries with the same id.** The runtime keys every node by its id:
  the last one wins, the earlier one disappears, and a wire that named it
  now points at the wrong node.
- **An instance of a subflow whose definition is not in the configuration.**
  The runtime installs a node type it cannot resolve, logs `Waiting for
  missing types to be registered` and leaves the flows stopped — measured
  above, against a real runtime.

The two topologies the runtime owns are left alone, and the tests say so: a
`link out` with a `links` array has its wires recomputed by the runtime, so
its wires are not checked; a `link in` may point at a `link out` in another
tab; and a subflow instance is an ordinary node of its tab, so its wires are
checked against that tab like any other node's.

**The recovery path.** `restore_backup` refuses a backup that no longer
validates and lists the problems; `force=true` installs it anyway and the
result says it was installed unvalidated. Nothing reaches the unchecked path
by accident: the client exposes it as a separate method, and the tool
exposes it as an explicit argument.

**A note on the shape of the fix.** The tab write path's guard
(`validateFlowWires`) refused a dangling wire, a malformed `wires` field
and an unreadable document; the subflow writes called none of it. Rather
than write a second set of rules, the subflow path now calls the same two
functions the tab path calls — the wires rule and the owner rule — and the
deployment path calls the same rule engine (`ValidateFlow`) per owner. The
only thing that is new is the rule set that only a whole configuration can
judge: the orphan node, the duplicate id and the missing subflow.

**A finding, not fixed here.** The complexity gate only checks the
functions named in scripts/complexity-baseline.txt — five of them. The tree
carries 25 functions over the threshold of 15, of which five are production
code that predates this work (`config.Load` at 26, `discoverContextHelper`
at 24, `handleInjectNode` at 18, and two from #332: `subflowReferences` at
19 and `InstantiateSubflow` at 17). This PR adds floors for the code it
adds, but the rest is a separate piece of work: the gate's coverage is
narrower than the rule it is meant to enforce.

## Next step
Open one PR linking #334 and merge it once CI is green.
