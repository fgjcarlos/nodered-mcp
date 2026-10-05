# Issue #316 — Reject known-incompatible operations before side effects

## Objective
Make `set_context` and the payload path of `inject_node` refuse known-incompatible
runtimes *before* any provisioning or mutation, reusing the version probe
and the minimum-version table that already exist.

## Problem and evidence
`nodered_min_version_for` (version_gate.go:23-27) is the documented single
source of truth: `set_context` and `inject_node` need NR ≥ 5.0.0,
`get_diagnostics` needs ≥ 3.1.0. Today that table only annotates tool
descriptions and feeds the capability matrix. `handleSetContext`
(tools_context.go:82) calls `ensureSetContextHelper` at line 128 — which
provisions a real helper flow tab, inject and function — with no version
check anywhere. A client that ignores the description gets a real write
attempt on an old runtime.

## The ladder rung this lands on
Everything needed already exists; this is enforcement, not new capability.

| Need | Already present |
|---|---|
| Version of the running Node-RED | `(*Client).NodeRedVersion(ctx)`, version.go:106 |
| Cost of asking | `sync.Once` cache — the first call probes `/settings`, later calls are free. No new probe. |
| Minimum per tool | `MinVersionFor` / `MinVersionForKnown`, version_gate.go:32,42 |
| Compare | `Version.AtLeast`, version.go:28 |
| Timeout wrapper | `probeCtx(parent, timeout)`, tools_runtime_info.go:201 |

So the diff is a guard, not a subsystem. Explicitly rejected: any new
version cache, any new probe, any new table, any change to the public
tool list.

## Design: the one non-obvious decision

**The `inject_node` gate must NOT go at the top of the handler.**

`handleInjectNode` (tools_flows.go, from line ~458) has two paths:
- no payload → `InjectNode(ctx, id)` at line 512. Works on every
  supported Node-RED. The issue requires preserving it.
- payload → `buildInjectPayloadBody` + `InjectNodeWithBody` at line
  522-523. This is the `__user_inject_props__` override that only
  behaves as documented on 5.0+.

A gate at function entry would refuse the no-payload path on an old
runtime and break a call that works. The guard belongs immediately before
the payload dispatch, so the refusal applies exactly to the behaviour
that is actually unsupported. This is the acceptance criterion
"preserve no-payload injection on versions where it actually works", and
it is the thing most likely to be got wrong.

`set_context` is different: the helper body also carries
`__user_inject_props__` (tools_context.go:149), so *all* of
`handleSetContext` depends on 5.0+. Its guard belongs before the
`ctxHelperMu` lock, so a refused call provisions nothing.

## Policy for unknown versions — decided, not left open
`Version.AtLeast` returns false when `Known == false`, so a naive
`if !v.AtLeast(...) { refuse }` would refuse every call on a runtime
whose version was never detected. That is a large regression: `/settings`
is frequently authenticated or absent, so detection failing is an
ordinary configuration, not an old Node-RED.

Policy: **refuse only what is *known* incompatible.** `Known == false`
proceeds, and the result text says the version could not be detected so
the caller knows the guarantee was not applied. This follows the issue's
own wording ("refuse *known-incompatible*", "a conservative, explicit
policy for unknown versions without inventing a successful capability
check"): we neither block a working runtime nor pretend the check
passed. The message must not claim the tool is available — only that it
was not proven incompatible.

## Scope and constraints
- Fix #316 only. Audited base is 086bf1a, now an ancestor of main.
- Additive response text is fine; do not change the public tool list.
- Refusals are `mcp.NewToolResultError`, matching sibling handlers.
- No new dependency; `go.mod` must not change.
- Keep functions under the repo's complexity-15 threshold (issue #73) —
  extract the guard rather than growing the handlers.
- English artifacts, no AI or co-author attribution.
- No push, PR or issue closure without authorization.

## Work unit
- [x] T1: Gate set_context and the inject payload path on the existing
  version table.
  - Route: delegated direct (2+ non-trivial files).
  - Risk: high — it is a refusal path in front of real writes. A wrong
    gate either breaks a working call or fails to block a bad one.
  - Acceptance: refusal precedes provisioning and any mutation; the
    refused call provably writes nothing (assert no HTTP write reached
    the fixture); no-payload inject still works on an old runtime;
    payload inject is refused on an old runtime; unknown version
      proceeds and says so; `ok`/newer runtime unaffected; the error
      names the required minimum and the detected version.
  - Verification: supported / unsupported / unknown for both tools, plus
    the no-payload-preserved case; full suite; race; vet; gofmt.
  - Rollback: revert the issue-scoped commits.

## Progress
Task document created before the first source write. Engram mirror
pending: this runtime exposes no Engram tools.

### Commits
- 8397da4 — gate + the non-probing cache accessor (writer, after the
  first attempt correctly stopped on 9 broken existing tests).
- cf2e8a0 — probed flag made race-free (parent; see below).
- docs comment on isLoopbackTestFixture (parent; see "Known gap").

### Parent verification after the writer's pass

The writer reported the accessor as "racy in principle, but in practice
the gate always runs after the banner goroutine". That is wrong, and the
race detector agreed once the access pattern was actually exercised. A
hammer test with 64 goroutine pairs calling both accessors produced:

```
WARNING: DATA RACE
Read at ... CachedNodeRedVersion  version.go:144
Previous write at ... NodeRedVersion.func1  version.go:115
```

`sync.Once` orders its own callers; a handler that reads the cache
without taking the Once is not one of them. "The probe runs at most
once" is a guarantee about the network call, not about field
visibility. Fixed with `atomic.Bool` for `probed`; the value stays a
plain field stored before the flag, which is what makes the
unsynchronised read sound.

An `atomic.Pointer[Version]` for the value was tried and reverted: the
sabotage that reverted `value` to a plain field still passed, proving
the atomic flag already supplies the happens-before. Machinery with no
additional guarantee is complexity.

Sabotage, both directions, in throwaway worktrees:
- revert the version reason to a generic string → 4 assertions fail
- revert `probed` to a plain bool → the race test fails
- revert the guards to `return false, ""` → "refused call reached the
  fixture" fires (verified by the independent reviewer)

### Known gap, documented not fixed (deliberate, out of #316's scope)

`isLoopbackTestFixture` (server.go:320) skips the startup version
probe for any BaseURL starting `http://127.0.0.1` / `http://localhost`
/ `http://[::1]`. The shipped default for `NODERED_URL` is
`http://localhost:1880` (internal/config/config.go:137), so an ordinary
**local production install** matches the "test fixture" rule and never
warms the cache. On that deployment the #316 gate does not enforce,
silently.

Not fixed here: the correct fix is to invert the heuristic into an
explicit opt-in (`Options.NoBannerProbe`) so tests declare themselves
rather than production being guessed at from a URL string. That touches
the server constructor and every strict test fixture — a change of its
own, and #316 is about the gate, not about probe policy. The comment on
`isLoopbackTestFixture` now states the hazard and the upgrade path so
the next person does not read the loopback branch as "tests only".

The gate remains fail-open by design, and a call that proceeds without a
version check carries `UnknownVersionNotice`, so a caller is at least
told no guarantee was applied.

### Closure verification (parent, this session)

T1 closed. Risk tier high, so verification was proportionate: writer
self-verification plus an independent read-only verifier for the full
suite, and a parent sabotage in both directions.

- `go build ./...`, `go test ./... -count=1` (5/5 packages),
  `go test -race ./internal/mcp/ ./internal/nodered/`, `go vet ./...`,
  `gofmt -l ./internal/` — all clean, **no DATA RACE warning**.
- Sabotage, parent-run, restored clean afterwards: neutering
  `RefuseForVersion` to never refuse produced
  `refused set_context should not hit POST /flow`,
  `refused call reached the fixture 1 times, want 0` and
  `refused inject_node(payload) should not probe /flows`. The tests
  assert real writes, not just the error string.
- Acceptance mapping confirmed by test name: known-too-low refuses,
  known-supported proceeds, unknown proceeds with notice, and
  `TestHandleInjectNode_NoPayloadStillFiresOnTooLowVersion` pins the
  one case a top-of-handler gate would have broken.

Confirmed by grep that the documented gap is the *only* path that
leaves the cache cold: the banner probe (server.go:286) and
`get_runtime_info` (tools_runtime_info.go:108) are the only two
callers of `NodeRedVersion`, and every gate reads
`CachedNodeRedVersion`. So on a `localhost` deployment the gate does
not engage until something calls `get_runtime_info` — a narrower
claim than "never", and the `UnknownVersionNotice` is the only signal
during that window.

## Next step
T1 is the whole of #316. Remaining work is delivery, and it is the
user's call: push the branch, open the PR, close the issue.

Verified by reading before delegating:
- `NodeRedVersion` is `sync.Once`-cached (version.go:89-114) and skips
  the probe entirely when `baseURL` is empty, so test fixtures that
  build a bare `Client{}` are unaffected.
- `AtLeast` explicitly returns false for `!Known` (version.go:29) — the
  trap behind the unknown-version policy above.
- `ensureSetContextHelper` is called at tools_context.go:128, after the
  lock at 126; the guard must precede both.
- `InjectNode` (no payload) at tools_flows.go:512; the payload dispatch
  at 522-523.
- `probeCtx` already exists at tools_runtime_info.go:201; reuse it
  rather than writing another timeout helper.
- 086bf1a is an ancestor of origin/main (830b481).
