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
and reaches the normalization.

**What the panic costs, measured.** Neither transport lets the process die, and
that is worth stating precisely:

- Over HTTP, the repository's own `recoverPanics` wrapper
  (internal/mcp/http_observe.go) turns the panic into a 500 "internal server
  error".
- Over stdio, mcp-go v0.58.0 recovers the panic in its tool-call worker
  (server/stdio.go) and answers JSON-RPC `-32603` with "internal panic:
  assignment to entry in nil map". The server survives and keeps serving.

So the damage is not a crash: it is an opaque internal error where a
validation error belongs, plus a panic and a stack trace in the log on every
such call. The first version of this record claimed the stdio process went
down; it does not, and the claim was measured rather than assumed.

**What the update path did, measured rather than assumed.** `update_flow`
normalizes with the fill disabled, so it never reached the write. It was
already refused, by the revisioned-deploy guard in `flattenFlowUpdate`
(`doc.Extra == nil` → "flow document must be a JSON object"). An independent
measurement against a real Node-RED 5.0.8 over the stdio transport confirmed
it: the pre-fix build answered `flow document must be a JSON object` and the
runtime logged no deploy.

One detail of that measurement matters: the pre-fix path did read the runtime
before refusing. The revisioned deploy takes its base from `GET /flows`, so
the refusal came after a read — there was no deploy, but there was a request.
The fix now refuses the same document before the runtime is contacted at all.
It does not close a hole on that path; it moves the refusal earlier and makes
the message name the shape.

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
Actual: 448 changed lines (89 fix, 227 tests, 14 baseline, 118 record).
Strategy: ask-on-risk, one PR, two commits (the fix, then the extraction the
  complexity gate required).

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
  is what the caller asked for. Measured against a real Node-RED 5.0.8:
  `POST /inject/:id` with `{"payload":null,"__user_inject_props__":[]}`
  answers 200 and the message fires with a null payload. Refusing instead
  would have been wrong: a body of `{"payload":null}` without the marker is
  silently discarded by the runtime, and a literal null body is a 400 from
  body-parser's strict mode.

RED observed as a real panic on the three paths before the fix — `create_flow`,
`import_flow` and `inject_node` — with `panic: assignment to entry in nil
map`, re-derived against the pre-fix tree (`main` @ e7b0507) rather than
against a half-reverted copy. GREEN after. The regressions cover the
normalization helpers directly and the tool boundaries (`create_flow`,
`import_flow`, `update_flow`, `inject_node`), and they assert that no request
reaches the runtime for a refused document.

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

### The rest of the class
A scan of the whole tree for "unmarshal into a map, then write into it" found
one more site, one line away from the same panic:
`synthesizeFlowFromFlat` (internal/nodered/flows.go) writes `tab["nodes"]`,
`tab["configs"]` and `tab["id"]` into a map decoded from the tab element. It
is not reachable today — only an element with `"type":"tab"` is picked, and
that is always an object, so the map is never nil — but the guard costs a
helper and removes the trap.

A second pass over the tree — this time with an AST scan rather than a
pattern — found five more places that write into a decoded map. None of them
is a live trap, and each is listed here so the claim is not "nothing else
writes into a decoded map" but the more useful "here is every one of them and
here is what keeps it safe":

- `cmd/nodered-mcp/init.go` — `root[rootKey] = servers`; the root map comes
  from `parseJSONObject`, which replaces a nil map with an empty one.
- `internal/nodered/flows.go` — `doc.Extra["disabled"] = …`; guarded by an
  explicit `if doc.Extra == nil`.
- `internal/nodered/flows_update.go` — `doc.Extra["id"]` and `doc.Extra["type"]`;
  guarded the same way.
- `internal/nodered/edit.go` — `node["wires"] = …` in `setWires`; the node
  always comes from a decoded object, so the map is never nil.
- `internal/nodered/flows_update.go` — the overlay maps in `overlayFlowEntry`;
  their entries come from `indexFlowEntries`, which rejects a null entry.

The `import_flow` clipboard path was found by reading the callers of
`normalizeFlowDoc` rather than by the scan: it is the second reachable panic,
and it is now covered by a regression of its own.

## Next step
Open one PR linking #333 and merge it once CI is green.
