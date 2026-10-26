# Issue #333 — Handle JSON null without panicking in flow and inject normalization

## Objective
Make a JSON `null` document or payload produce a validation error instead of a
panic. `json.Unmarshal("null", &map)` returns **no error** and leaves the map
**nil**, and the next write into that map panics with `assignment to entry in
nil map`. The rule has to be coherent across the two surfaces that take JSON:
a flow document is refused, an inject payload is wrapped.

## Problem and evidence
Two write paths decoded a caller-supplied JSON value into a map and then wrote
into it:

- `normalizeFlowDoc` (internal/mcp/tools_shared.go) → `coalesceNullFields` →
  `doc["nodes"] = …`. With the fill enabled, a `"null"` document panicked
  there. With the fill disabled — the update path — it did not panic but
  handed a `null` document to the runtime, which answers with an opaque
  "Cannot read properties of null".
- `buildInjectPayloadBody` (internal/mcp/tools_flows.go) →
  `m["__user_inject_props__"] = …`. A `"null"` payload panicked there.

Both are reachable from the tool surface: the argument is a JSON-encoded
string, `json.Valid("null")` is true, so the value passes argument validation
and reaches the normalization. The panic is not confined to one transport —
the HTTP transport has a `recoverPanics` wrapper that turns it into a 500,
while the stdio transport has no such wrapper, so the process goes down.

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
Actual: 262 changed lines (23 fix, 144 tests, 95 record).
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

RED observed as a real panic on both paths before the fix: `panic: assignment
to entry in nil map`. GREEN after. The regressions cover the normalization
helpers directly and both tool boundaries (`create_flow`, `update_flow`), and
they assert that no request reaches the runtime for a refused document.

### A third site of the same class
A scan of the whole tree for "unmarshal into a map, then write into it" found
one more site, one line away from the same panic:
`synthesizeFlowFromFlat` (internal/nodered/flows.go) writes `tab["nodes"]`,
`tab["configs"]` and `tab["id"]` into a map decoded from the tab element. It
is not reachable today — only an element with `"type":"tab"` is picked, and
that is always an object, so the map is never nil — but the guard costs one
line and removes the trap.

Everything else that decodes into a map only *reads* from it, which is safe
on a nil map.

## Next step
Push the branch, open one PR linking #333, and merge once CI is green.
