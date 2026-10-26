# Issue #334 — Validate the effective configuration before a full or subflow deployment

## Objective
A deployment must not install a configuration whose references do not resolve.
`set_flows` (a full deployment) and `restore_backup` deployed whatever they
were given, the subflow writes did not look at the topology of the
definition at all, and a write of a single tab installed a whole array
without being judged as one. The rules the tab write path already applies
have to be shared, without refusing the topologies the runtime owns: link
nodes and subflow instances. A restore of a configuration that no longer
validates needs an explicit, documented recovery path.

## Problem and evidence
The audit fixture received a deployment with `wires:[["missing"]]` and the
client returned success. That is the whole defect: the write path had no
guard at all on the paths that install a whole configuration.

**Measured against a real Node-RED** (installed under /tmp, admin API on
127.0.0.1:1880; the measurements below reproduce on 5.0.8 and on 5.0.9),
not assumed:

| Probe | Request | Result |
|---|---|---|
| Full deploy with a dangling wire | `POST /flows` (full) with `wires:[["missing"]]` | **HTTP 204**, the wire is stored as it was, the client reported success |
| Full deploy with a subflow instance and no definition | `POST /flows` with a node of type `subflow:missing-id` and no definition | **HTTP 204**, and then the runtime logs `Waiting for missing types to be registered: - subflow:missing-id` and **the flows stay stopped** |
| Full deploy with a subflow whose `in` port names a node that is not in it | `POST /flows` with `"in":[{"wires":[{"id":"ghost","port":0}]}]` | **HTTP 204**, and then the runtime throws `TypeError: Cannot read properties of undefined (reading 'id')` (@node-red/runtime/lib/flows/Subflow.js:223) while it starts the flows — the deployment said success and the flows did not start |
| Full deploy with a subflow definition and an instance of it | `POST /flows` with the definition, its inner node and the instance | HTTP 204; `GET /flows` returns **the definition and its inner node in the flat array**, and `GET /flow/global` returns the same definition with its nodes nested |
| A node whose `z` names no tab | `POST /flows` with `z:"nowhere"` | HTTP 204, the flows start, and the node is stored with that `z` — the runtime invents a tab for it (@node-red/runtime/lib/flows/util.js, parseConfig) and the node runs outside the flow that was deployed |
| Two entries with the same id | `POST /flows` with two nodes called `n1` | HTTP 204, the flows start, and the runtime keys every node by its id, so the last one wins and the earlier one is gone |

The second and third rows are the ones that matter most: a deploy the
client called a success left the runtime **stopped**, or made it throw.

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
`link out` with a truthy `links` array has its wires rewritten by the
runtime. The test is the runtime's: it checks the field for truthiness, so
a `links` field of `null` leaves the wires alone and they are checked like
any other node's.

**One thing this record got wrong first time, and the review caught it.**
The first version of this record justified the cross-tab wire rule with
"the runtime resolves wire targets inside their owner, so the message is
dropped and the wire is dead". That is false. `Flow.getNode` falls back to
the parent flow and the global `getNode` scans every active flow
(@node-red/runtime/lib/flows/index.js), and a message injected in one tab
was received by a debug node in another one, against a live runtime. The
rule is kept — it is the repository's rule and the per-tab write path has
always applied it — but it is stated as what it is (a rule, stricter than
the runtime) and not as a fact about the runtime, and `set_flows` grew the
same explicit escape hatch the restore has.

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
- A write of a single tab is judged **in the context of the whole
  configuration**, but only the entries of the tab being written are the
  caller's: the rest is the runtime's own state, and refusing it would
  make the runtime impossible to repair.
- The recovery path is explicit. `restore_backup` and `set_flows` refuse a
  configuration that does not validate and `force=true` installs it anyway;
  the result says it was installed unvalidated. The node-type policy and
  the rule that a deployment must leave at least one tab are **not**
  skipped by it.
- The rules the runtime owns are not re-litigated: link wires and
  cross-tab link references are legal and stay legal.
- **Out of scope, stated here because the harm is real:** a node whose type
  is not registered in the runtime (`totally-not-a-real-type`) also leaves
  the runtime stopped, with the same "waiting for missing types to be
  registered" log. It is deliberately not refused. The runtime treats a
  missing node type as a *recoverable* state — install the node and the
  flows start — so refusing the deployment would block the very deploy
  that an operator needs to make once the node is installed. A missing
  *subflow definition* is the opposite: nothing can register it later, so
  that one is refused. The difference is deliberate.

## Work unit
- `internal/nodered/validate_deploy.go` (new): `ValidateEffectiveConfig`
  and the rules — the dangling wire, the orphan node, the duplicate id, the
  subflow instance without a definition, the port of a subflow that names
  a node which is not in it, and the link node whose `links` field is not
  an array. The per-owner rules are the ones `ValidateFlow` applies,
  applied to a document rebuilt per owner (a tab or a subflow definition),
  so the tab path and the deployment path judge a wire by the same rule.
  The configuration is read once into an index, so the validation is
  linear in its size.
- `internal/nodered/validate.go`: `FlowGuardError`, a typed refusal that
  keeps the message the write path has always produced and says that the
  runtime was never contacted.
- `internal/nodered/flows.go`: `validateFlowWires` and `validateZRefsInFlow`
  return that typed error; `RestoreFlows` validates the backup and
  `RestoreFlowsUnchecked` is the documented recovery path; the shared body
  moved into `restoreFlowsLocked`.
- `internal/nodered/settings.go`: `SetFlows` validates the array it is
  about to deploy, before the snapshot and before the runtime is contacted;
  `SetFlowsUnchecked` is its explicit escape hatch. The "at least one tab"
  rule now reads the entries instead of searching for the bytes
  `"type":"tab"`, which refused a payload whose JSON was formatted with a
  space after the colon.
- `internal/nodered/flows_update.go`: a write of a single tab is judged in
  the context of the whole configuration.
- `internal/nodered/subflows.go`: `CreateSubflow` and `UpdateSubflow` apply
  the guard the tab path applies — the wires rule and the owner rule — to
  the definition they are about to write, and the port rule. The guard runs
  after the id check, so a body whose id does not match the path id is
  still reported as that.
- `internal/mcp/config_validation.go` (new): the two renderings. A refusal
  by the effective-configuration guard lists the problems with their kind;
  a refusal by the per-document guard says the runtime was not contacted.
- `internal/mcp/tools_backups.go`: `restore_backup` takes `force`.
- `internal/mcp/tools_runtime.go`: `set_flows` takes `force`.
- `internal/mcp/tools.go`, `docs/tools.md`: the descriptions advertise the
  guard and the recovery path, so a caller knows both before it tries.

## Delivery
- Tests: 27 unit tests for the rules (internal/nodered/validate_deploy_test.go),
  14 write-path tests (internal/nodered/validate_deploy_write_test.go) and
  7 tool-boundary tests (internal/mcp/config_validation_test.go).
- The complexity gate now covers the new code: floors for the new named
  functions are added to scripts/complexity-baseline.txt. No existing
  floor moves, and the PR adds no function above the threshold — the tree
  has the same 25 functions over 15 before and after (the gate only checks
  the functions named in the baseline, which is a known narrowness of it).
- The user-visible text of four existing write tools changes: a refusal by
  the guard now reads "the write was refused before the runtime was
  contacted: …" instead of "calling Node-RED: …". The old text claimed
  Node-RED had been called when it never was.

## Progress
**RED**, re-derived against the tree before the fix (`main` @ 5c1791b, in a
separate checkout) rather than against a half-reverted copy: all three
paths deployed the broken configuration.

```
--- FAIL: TestRED_SetFlows_DanglingWireIsRefused       RED: expected SetFlows to refuse a dangling wire, got nil
--- FAIL: TestRED_RestoreFlows_DanglingWireIsRefused   RED: expected RestoreFlows to refuse a dangling wire, got nil
--- FAIL: TestRED_CreateSubflow_DanglingWireIsRefused  RED: expected CreateSubflow to refuse a dangling wire, got nil
--- FAIL: TestRED_CreateSubflow_DanglingPortIsRefused  RED: expected CreateSubflow to refuse a dangling subflow port, got nil
```

**GREEN** on the branch: the same four tests pass, plus 44 more.

The rules, and what each one is for:

- **A wire that names no node in the same owner.** This is the
  repository's rule, shared with the per-tab write path. The runtime does
  resolve such a wire across tabs — the review proved it with a live
  message delivered from one tab to a debug node in another — so the rule
  is stricter than the runtime. The editor never creates one, and both
  write paths refuse it, so the configuration an operator installs and the
  configuration they can then edit are judged by the same rule.
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
- **A port of a subflow definition that names a node which is not in it.**
  The runtime resolves the ports against the definition's own members when
  it starts an instance: an `in` port that names a node which is not there
  makes the start throw, and an `out` port that names one logs `Unknown
  node referenced inside subflow` and the output never fires. The
  deployment answers 204 in both cases, so the deployment looks like it
  worked while the flows do not start — measured above, against a real
  runtime. The one legal reference that is not a member is the
  definition's own id on an `out` port, and the guard leaves it alone.
- **A link node whose `links` field is not an array.** The runtime
  iterates that field with forEach when it parses the configuration, so a
  truthy value that is not an array stops the deploy with a TypeError.

The two topologies the runtime owns are left alone, and the tests say so: a
`link out` with a truthy `links` array has its wires recomputed by the
runtime, so its wires are not checked; a `link in` may point at a `link out`
in another tab; and a subflow instance is an ordinary node of its tab, so
its wires are checked against that tab like any other node's.

**The recovery path.** `restore_backup` and `set_flows` refuse a
configuration that does not validate and list the problems; `force=true`
installs it anyway and the result says it was installed unvalidated.
Nothing reaches the unchecked path by accident: the client exposes it as a
separate method and the tool exposes it as an explicit argument. The
node-type policy is not skipped by it — that is a security refusal, not a
rule about references.

**A note on the shape of the fix.** The tab write path's guard
(`validateFlowWires`) refused a dangling wire, a malformed `wires` field
and an unreadable document; the subflow writes called none of it. Rather
than write a second set of rules, the subflow path now calls the same two
functions the tab path calls — the wires rule and the owner rule — and the
deployment path calls the same rule engine (`ValidateFlow`) per owner. The
only thing that is new is the rule set that only a whole configuration can
judge: the orphan node, the duplicate id, the missing subflow, the subflow
port and the malformed links field.

**What the review changed.** An independent adversarial review of the
branch (against a real Node-RED, driven through the MCP over stdio) found
seventeen things. Three of them blocked the merge and are fixed here: the
false rationale about the cross-tab wire (F1), the unknown node type left
uncovered (F3, now stated as out of scope rather than implied to be
covered), and a write of a single tab installing a whole array with no
whole-configuration guard (F4, now judged in the context of the
configuration). The rest were one-line fixes: the truthiness of the
`links` field (F5), a duplicate owner reported twice (F6), a message that
quoted an empty `z` for a `z` that is not a string (F7), the "the runtime
throws" wording applied to an `out` port that only logs (F8), the
quadratic cost of the validation (F9), a restore that accepted a backup
with no tab — an empty backup emptied the runtime (F10), the changed
user-visible text (F11), a misleading error when a subflow body's id did
not match the path id (F12), and the numbers in this record (F13). The
review also measured the validation at 4.7s for a ten-thousand-entry
configuration; with the index it is 256ms.

**A finding, not fixed here.** The complexity gate only checks the
functions named in scripts/complexity-baseline.txt. The tree carries 25
functions over the threshold of 15, of which seven are production code
that predates this work (`config.Load` at 26, `discoverContextHelper` at
24, `subflowReferences` at 19, `matchesHelperNode` and `handleInjectNode` at
18, `InstantiateSubflow` at 17 and `(*Config).validate` at 16). This PR
adds floors for the code it adds, but the rest is a separate piece of work:
the gate's coverage is narrower than the rule it is meant to enforce.

## Next step
Open one PR linking #334 and merge it once CI is green.
