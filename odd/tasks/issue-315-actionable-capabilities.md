# Issue #315 — Actionable runtime capability diagnostics

## Objective
Make `get_runtime_info` tell the operator what to DO about a degraded
capability, not just name the state — reusing the facts the matrix
already computes, with no new registry, gate or command.

## Problem and evidence
`get_runtime_info` renders `mcp.capabilityMatrix` as
`map[string]string` (tools_runtime_info.go:35, filled at 59-61). A
client sees `"set_context": "version_too_low"` and has to know, out of
band, that it means "upgrade Node-RED to ≥ 5.0.0".

The facts needed to make that actionable already exist and are already
computed:

| Capability | Source of truth | Already available |
|---|---|---|
| `version_too_low` | `nodered_min_version_for` (version_gate.go:23) | `MinVersionFor(tool)` |
| `setting_disabled` | `settings.runtimeState.enabled` from `/settings` | `RuntimeProbe.RuntimeStateEnabled` |
| `endpoint_not_mounted` | probe result | `RuntimeProbe.RuntimeLogsMounted` |
| `stream_disabled` | `s.debugStream` | `RuntimeProbe.DebugStreamEnabled` |
| `unknown` | version not parseable | `RuntimeProbe.NodeRedVersion.Known` |

So this is a presentation change over facts that are already in hand,
not new detection. That is exactly what the issue asks for and what its
"Do not add another CLI command, registry, or mandatory gate" constraint
demands.

`classifySettingTool` (capability.go:85) discards *which* setting was
closed, and `classifyVersionedTool` (71) discards the minimum. Both are
recoverable without new state because the probes carry them.

## Design chosen
Add a `reason` (and `remedy` where a concrete next step exists) next to
each non-`ok` capability, derived from the same probe that produced the
state. `ok` entries get no reason — absence is the signal.

Implementation seam: a pure function next to the matrix that maps
`(tool, capability, probe)` to a remediation string. Pure, no I/O, same
shape as the classifiers it sits beside, testable in isolation. The
handler gains one field and one loop.

Deliberately NOT done, per the issue and per the retirement of the
doctor framework (#229/#247):
- No new capability constants. The vocabulary in capability.go:14-21 is
  documented as tight and greppable; reusing an existing constant to mean
  "version unknown" would corrupt the audit trail.
- No new endpoint probing. Every reason must come from a probe already
  run, or it is not stated. Reporting an assumption as a fact is the
  failure mode the issue names.
- No new CLI command and no second capability registry.

## Scope and constraints
- Fix #315 only.
- Existing JSON field names stay stable; the new fields are additive.
  A downstream consumer must not break.
- `ok` and `unknown` must remain distinguishable, and `unknown` must
  stay explicit — the issue requires unknown runtime to be visible, not
  smoothed over into "probably fine".
- No new dependency; `go.mod` must not change.
- No push, PR or issue closure without authorization.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Add actionable reason/remedy to the capability matrix output.
  Commits 10c0c57 (writer) + dfdca4a (parent, dropped a duplicated
  probe comment the writer left in tools_runtime_info.go).
  - Route: delegated direct (capability.go + the handler + tests).
  - Risk: medium — the response shape is a public contract with a
    stated downstream consumer, and an inaccurate reason is worse than
    no reason.
  - Acceptance: a reason for every non-`ok` capability; `ok` carries
    none; `unknown` explicit and never phrased as a fact; the version
    reason names the actual minimum from `nodered_min_version_for`; no
    probe is run that the handler did not already run.
  - Verification: tests for old runtime (version_too_low with the real
    minimum), current runtime (ok, no reason), unknown runtime, the
    setting-disabled reason naming the actual setting, and the
    endpoint-not-mounted reason; existing suite, `go test ./...`,
    `go test -race ./...`, `go vet ./...`, gofmt.
  - Runtime proof: `httptest` fixtures as the existing runtime-info
    tests use; no live Node-RED.
  - Rollback: revert the one issue-scoped commit.
  - Observed: gofmt/vet/full/race green, go.mod untouched.
- [x] T2: Document the response in docs/tools.md. Commit 134f362.
  - Route: inline (docs only, passive).
  - Acceptance: the two maps, why `ok` has no entry, why `unknown` has
    no remedy, and that reasons come only from existing probes.
  - Verification: structural readback.

## Delivery
Forecast: roughly 100-200 authored changed lines including tests.
Actual: 3 commits, ~430 changed lines, no new dependency.
Strategy: ask-on-risk, one PR. Push and PR remain the maintainer's
decision.

## Progress
Task document created before the first source write. Engram mirror
pending: this runtime exposes no Engram tools.

Verified before coding:
- `runtimeInfo.MCP.CapabilityMatrix` is `map[string]string`
  (tools_runtime_info.go:35). The reason fields are additive; the
  existing map keeps its type so current consumers keep working.
- The handler already runs every probe the matrix needs, in
  `runtimeProbe` (tools_runtime_info.go:81). No new probe is required
  for any of the five capabilities.
- `MinVersionFor` / `MinVersionKnown` (version_gate.go:32,40) already
  expose the minimum per tool — the version reason needs no new lookup.
- Capability vocabulary is five constants (capability.go:14-21) with a
  comment saying to add a new constant rather than reuse one, because
  an audit greps those strings. Do not extend it for presentation.
- `noderedCapabilityMatrix` is already pure and unit-tested in
  isolation; the new function should match that shape.

### Parent verification after the writer's pass

Read the diff rather than the report. Confirmed by reading the source:
- `capabilityGuidance` routes the version case through
  `MinVersionForKnown(tool)`, so the reason follows the gate table
  automatically and the next tool added to `nodered_min_version_for`
  inherits the right copy with no code change.
- `MCP_DEBUG_STREAM` and `--debug-stream` are real (cmd/nodered-mcp/main.go:75,119)
  — the writer did not invent the flag names.
- `settings.runtimeState.enabled` is the exact key
  `parseRuntimeStateEnabled` (tools_runtime_info.go:185) parses, not a
  plausible-sounding invention.
- `capabilityMatrix` keeps its `map[string]string` type; the new
  `capabilityGuidance` map is a separate additive field.

Sabotage in a throwaway worktree (first attempt was blocked by the
command scanner and left the repo untouched — verified afterwards that
`capability.go` still held the specific reason and no stray backup
existed). Degrading the version reason to a generic string fails the
test on four separate assertions, including the one that two tools must
not produce identical guidance:

```
--- FAIL: TestCapabilityGuidance_OldRuntimeNamesCorrectMinPerTool
    set_context reason should name 5.0.0 minimum
    get_diagnostics reason should name 3.1.0 minimum
    inject_node reason should name 5.0.0 minimum
    set_context and get_diagnostics produced identical reasons; guidance must be tool-specific
```

