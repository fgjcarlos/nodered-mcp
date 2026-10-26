# Issue #333 — Handle JSON null without panicking in flow and inject normalization

## Objective
Make a JSON `null` document or payload produce a validation error instead of a
panic. `json.Unmarshal("null", &map)` returns **no error** and leaves the map
**nil**, and the next write into that map panics with `assignment to entry in
nil map`. The rule has to be coherent across the two surfaces that take JSON:
a flow document is refused, an inject payload is wrapped.

## Problem and evidence
Three write paths decoded a caller-supplied JSON value into a map and then
wrote into it:

- `normalizeFlowDoc` (internal/mcp/tools_shared.go) → `coalesceNullFields` →
  `doc["nodes"] = …`. The fill is enabled on two tool paths, and a `"null"`
  document panicked on both of them: `create_flow` and `import_flow` (the
  latter through a clipboard whose single element is null).
- `buildInjectPayloadBody` (internal/mcp/tools_flows.go) →
  `m["__user_inject_props__"] = …`. A `"null"` payload panicked there, on
  `inject_node`.

All three are reachable from the tool surface: the argument is a JSON-encoded
string, `json.Valid("null")` is true, so the value passes argument validation
and reaches the normalization. The panic is not confined to one transport —
the HTTP transport has a `recoverPanics` wrapper that turns it into a 500,
while the stdio transport has no such wrapper, so the process goes down.

**What the update path did, measured rather than assumed.** `update_flow`
normalizes with the fill disabled, so it never reached the write. It was
already refused, by the revisioned-deploy guard in `flattenFlowUpdate`
(`doc.Extra == nil` → "flow document must be a JSON object"), before any
request reached the runtime. An independent measurement against a real
Node-RED 5.0.8 over the stdio transport confirmed it: the pre-fix build
answered `flow document must be a JSON object` and the runtime logged no
deploy. The fix now refuses the same document earlier, and with a message
that names the shape; it does not close a hole on that path.

Audit baseline: main @ 93f47b1 (audit-2026-10-19, area A06, P1).

## Scope and constraints
- Fix #333 only. No change to the node denylist, the wire validation, or the
  backup snapshot/prune safeguards.
- No new dependency, no change to the tool surface, no change to the shape of
  the bodies the runtime receives for a *valid* payload.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Refuse a null flow document and wrap a null inject payload.
  - Route: delegated direct.
  - Risk: high — the defect is a panic, and on the stdio transport a panic
    takes the process down.
  - Acceptance: see issue #333 bullets; null, arrays, booleans and scalars are
    processed or refused with no panic, in stdio and HTTP.
  - Verification: focused regression RED (observed as a real panic) then GREEN,
    `go test ./...`, `go test -race ./...`, `go vet ./...`, gofmt.
  - Runtime proof: local httptest fixtures; the defect is a decode, not a
    runtime interaction, so no real Node-RED is needed for the reproduction.
  - Rollback: revert the one issue-scoped commit.
  - Commit: the fix, its regressions, and this record.

## Delivery
Forecast: roughly 60–120 authored changed lines; advisory estimate.
Actual: 291 changed lines (60 fix, 203 tests, 28 baseline and record).
Strategy: ask-on-risk, single commit, one PR.

## Progress
Task document created before the first source write.

Root cause: the Go JSON decoder treats a document-level `null` as a successful
decode into a nil map, and neither of the two helpers checked for that before
writing into the map.

Fix, and the rule it follows:

- **A flow document is refused.** `null` is not a flow document, so
  `normalizeFlowDoc` answers with `flow document is null; expected a JSON
  object or a flow array`. This holds with and without the fill: the
  update path used to hand a null document to the runtime, which is a
  write with an opaque error, not a validation error.
- **An inject payload is wrapped.** The payload path already wraps every
  value that is not a JSON object — a scalar, an array — under `"payload"`,
  so the caller's value still reaches the inject's msg. A null payload is a
  scalar by that rule, and wrapping it keeps the two paths coherent: the
  inject receives `{"payload":null,…}` and fires with a null message, which
  is what the caller asked for.

RED observed as a real panic on all three paths before the fix: `panic:
assignment to entry in nil map`, re-derived against the pre-fix tree
(`main` @ e7b0507) rather than against a half-reverted copy. GREEN after. The
regressions cover the normalization helpers directly and the three tool
boundaries (`create_flow`, `import_flow`, `update_flow`), and they assert that
no request reaches the runtime for a refused document.

### The complexity gate
`scripts/complexity-check.sh` fails a pull request whose named functions
exceed the threshold of 15 **or** go up against their baseline, and the
baseline file is not to be bumped as a side effect of a refactor. The first
version of this fix added a branch to `normalizeFlowDoc` (6 → 7) and one to
`synthesizeFlowFromFlat` (15 → 16, over the threshold), so CI was red on the
complexity check. Both were resolved by extraction, which is the pattern the
repository already uses for this: the null check moved into
`decodeFlowDocument`, the object branch into `normalizeFlowObject`, the tab
decode into `decodeTabElement`. `normalizeFlowDoc` is now 3 and
`synthesizeFlowFromFlat` is back to 15, and the floor was regenerated with
`scripts/complexity-check.sh --write` so it matches reality again.

### A third site of the same class
A scan of the whole tree for "unmarshal into a map, then write into it" found
one more site, one line away from the same panic:
`synthesizeFlowFromFlat` (internal/nodered/flows.go) writes `tab["nodes"]`,
`tab["configs"]` and `tab["id"]` into a map decoded from the tab element. It
is not reachable today — only an element with `"type":"tab"` is picked, and
that is always an object, so the map is never nil — but the guard costs a
helper and removes the trap.

Everything else that decodes into a map only *reads* from it, which is safe
on a nil map. The `import_flow` clipboard path was found by reading the
callers of `normalizeFlowDoc` rather than by the scan: it is the second
reachable panic, and it is now covered by a regression of its own.

Everything else that decodes into a map only *reads* from it, which is safe
on a nil map.

## Next step
Push the branch, open one PR linking #333, and merge once CI is green.
