# Issue #52 — `set_context` via managed helper inject node

**Status:** Implementation complete. PR opened.

**Date:** 2026-07-28

## Approach (chosen)

The helper is one **`inject` node** + one **`function` node** on a dedicated
flow tab the MCP owns. The MCP lazy-provisions them on the first
`set_context` call and reuses them across all subsequent calls. The MCP
exposes `set_context` as an MCP tool; clients call it the same way they
call any other tool in the catalog.

**Why not just an `inject` node alone:** Node-RED 5.x's inject node's input
handler always ends in `RED.util.setMessageProperty(msg, ...)` — it can only
set `msg` fields, never `flow`/`global`/`context`. The actual write has to
happen in a downstream function node whose body dispatches on `msg.scope`.

**Why a persistent helper, not a fresh node per call:** per-call nodes mean a
write, a deploy, and a write per `set_context` — noisy, slow, and visible
in the user's flow. A one-time helper keeps the runtime untouched across
invocations and matches the issue's "reused, not recreated" requirement.

**Concurrency:** the helper is shared across all goroutines that call
`set_context` concurrently. `provisioningMu` guards the lazy provisioning
step; after it is provisioned, the helper ids are immutable, and concurrent
POSTs to `/inject/:id` are serialised by the runtime (which already
serialises inject triggers per node id).

**`scope=flow` / `scope=node` write target:** the function node can only
write to its own flow context and its own node context. The error message
for a foreign id names the only legal id (a deterministic constant the MCP
exposes), so a caller can fix the call without reading the source.

## Design tension resolved

The handler's per-scope id check runs **before** any server call. The check
compares against the helper's **deterministic constants** (e.g.
`setContextHelperFlowID = "mcp_ctx_helper_tab"`), not the runtime-assigned
ids. Rationale: Node-RED's `POST /flow` reassigns the tab id, and the MCP
keeps the new id internally — a caller never sees the runtime id. Using
the constants lets the caller pre-flight the check without provisioning
anything. The actual `InjectNodeWithBody` call still uses the runtime-assigned
`helper.injectID` so the runtime side is correct.

## Files changed

| File | Change |
|---|---|
| `internal/mcp/setcontext.go` | **NEW** — `setContextHelper`, `ensureSetContextHelper`, `provisionSetContextHelper`, `extractFlowID`, the `setContextFunctionCode` constant |
| `internal/mcp/tools.go` | `set_context` tool registered via `addWriteTool` (~50 lines); `handleSetContext` with the per-scope id check pre-provisioning (~120 lines); `prettyJSONValue` helper |
| `internal/mcp/server.go` | `ctxHelper` field on `Server`, propagated from `New` |
| `internal/mcp/tools_test.go` | 6 new tests (happy-path + helper reuse, bad scope, bad JSON, requires id for flow, foreign id rejected, withheld in read-only) |
| `internal/nodered/client_test.go` | Test coverage for `CreateFlow` id extraction and edge cases |
| `internal/nodered/flows.go` | `CreateFlow` signature/doc tightened; new `RawFlow` type already present |

## Tests added

- `TestSetContext_HappyPathAndHelperReuse` — 2 calls; asserts exactly 1
  `CreateFlow` (helper provisioned once) and exactly 2 `InjectNodeWithBody`
  dispatches; verifies the per-call value reaches the inject body.
- `TestSetContext_RejectsBadScope` — `scope=everywhere` rejected without
  hitting the server.
- `TestSetContext_RejectsBadJSONValue` — non-JSON `value` rejected without
  hitting the server.
- `TestSetContext_RequiresIdForFlow` — `scope=flow` without `id` rejected
  with a clear message naming the legal id.
- `TestSetContext_FlowScopeRejectsForeignId` — `scope=flow` with a foreign
  `id` rejected without hitting the server; error names the legal id AND
  echoes the bad id back.
- `TestSetContext_WithheldInReadOnly` — `set_context` is not advertised on
  a read-only server's tool surface (it is mutating).

## Verification

```
$ go test ./... -count=1
ok    github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp   0.011s
ok    github.com/fgjcarlos/nodered-mcp/internal/config  0.003s
ok    github.com/fgjcarlos/nodered-mcp/internal/mcp     0.117s
ok    github.com/fgjcarlos/nodered-mcp/internal/nodered 0.128s
ok    github.com/fgjcarlos/nodered-mcp/internal/oauth   0.403s

$ go vet ./...
(no output — clean)

$ gofmt -l .
(no output — clean)
```

## Out of scope (per the issue)

- Multi-key writes in one call (caller's job — fire two `set_context` calls).
- Reading back the value after write (covered by `get_context`).
- Cross-tab flow writes (`scope=flow` only targets the helper's own flow
  context; this is a Node-RED admin-API limitation, not a tool limitation).

## PR

- **URL:** <filled in by the committer>
- **Branch:** `feat/issue-52-set-context` → `main`
- **Title:** `feat(mcp): set_context via managed helper inject node (#52)`
