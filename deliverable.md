# Issue #51 — `get_runtime_logs` + `get_node_status`

**Status:** Implementation complete. PR opened.

**Date:** 2026-07-28

**PR:** https://github.com/fgjcarlos/nodered-mcp/pull/61

**Branch:** `feat/issue-51-runtime-observability`

## Approach (chosen)

Two new MCP tools, each using the same pattern the codebase already
follows (HTTP client to admin API + thin handler in `internal/mcp`):

1. **`get_runtime_logs`** — wraps `GET /logs?count=N`. The handler parses
   the response permissively (envelope `{logs: [...]}`, bare array, or
   plain text — the runtime is free to return any of them).
2. **`get_node_status`** — caches last-known status per node, fed by a
   new `StatusTail` that subscribes to the `status/#` topic on the
   existing `/comms` WebSocket.

## Why the approach diverges from the issue

The issue proposed that `GET /logs` is a stock Node-RED admin endpoint.
**It is not.** Stock Node-RED 5.x does not expose a `/logs` admin endpoint
— the runtime log is written to stdout, and per-node log events stream
on the `/comms` WebSocket under the `event-log/<id>` topic. The Coder
verified this by inspecting the Node-RED source. We still implemented
`get_runtime_logs` because (a) the endpoint may exist on forks or
future versions, and (b) the same shape is what Node-RED's logging
plugins mount when configured.

A 404 from `GET /logs` is treated as the **expected** answer on stock
Node-RED and the handler surfaces an actionable operator hint rather
than a generic HTTP error.

For `get_node_status`, the WebSocket path is gated on the existing
`MCP_DEBUG_STREAM` flag for the same reason `get_debug_messages` is:
the `/comms` dial can crash the runtime on some Node-RED versions
(flagged in #39 and #47). When the flag is off, the tool returns a
clear "stream unavailable" hint.

## Files changed

| File | Change |
|---|---|
| `internal/nodered/logs.go` | **NEW** — `Client.GetRuntimeLogs`, `parseRuntimeLogs` (envelope / array / plain text), `normaliseLogs`, `IsLogsNotFound` |
| `internal/nodered/logs_test.go` | **NEW** — 12 tests covering the client (404, envelope, array, plain text, count clamp, default, server error) and the parsers (level normalisation, time coercion, fallback-to-text) |
| `internal/nodered/status.go` | **NEW** — `StatusTail`, `NewStatusTail`, `Run`, `Lookup`, `Snapshot`, `ParseFlowNodeIDs`, `Status` enum, `classify` |
| `internal/nodered/status_test.go` | **NEW** — 18 tests: classify() mapping, record semantics (last-write-wins, cleared events, LastError survives recovery), snapshot/lookup, consume parser, flow-id expansion, the reconnect path, the unreachable-server path, the context-cancel path, the full /comms round-trip via httptest |
| `internal/nodered/client.go` | Added `getRaw` helper for endpoints whose body may not be JSON-decodable; refactored `do` to share code with `doURL` |
| `internal/mcp/server.go` | Wired `StatusTail` into the Server (constructed and started alongside `DebugTail`); both gated on `MCP_DEBUG_STREAM` |
| `internal/mcp/tools.go` | Registered `get_runtime_logs` (read) and `get_node_status` (read) via `addReadTool`; added handlers and supporting helpers (`filterLogs`, `writeLogLine`, `renderNodeStatus`, `renderFlowStatus`, `formatStatusEntry`) |
| `internal/mcp/tools_test.go` | Added the 2 new tools to `readOnlyTools`; updated `totalTools` to 32; added 10 handler tests |

## Tests added

`internal/nodered/logs_test.go`:
- `TestGetRuntimeLogs_NotFoundOnStockNodeRed` — 404 from /logs surfaces as `*APIError` and `IsLogsNotFound` agrees.
- `TestGetRuntimeLogs_EnvelopeShape` — `{logs:[...]}` shape; count query is forwarded.
- `TestGetRuntimeLogs_BareArray` — bare array shape; level is normalised to {"info","warn","error"}.
- `TestGetRuntimeLogs_PlainText` — plain-text body falls through; blank lines dropped.
- `TestGetRuntimeLogs_EmptyBody` — 200 with empty body returns empty slice.
- `TestGetRuntimeLogs_ClampsCount` — runaway count capped to 1000.
- `TestGetRuntimeLogs_DefaultCount` — 0 → 100.
- `TestGetRuntimeLogs_RejectsServerError` — 5xx surfaces as APIError, NOT misclassified as not-found.
- `TestGetRuntimeLogs_AppliesBearerAuth` — Authorization header is sent.
- `TestParseRuntimeLogs_FallbackToText` — unknown JSON shape falls through to text.
- `TestParseRuntimeLogs_PreservesOrder` — order is preserved.
- `TestNormaliseLogLevel` — level vocabulary mapping.
- `TestPickTime` — RFC 3339 / epoch ms / absent key.

`internal/nodered/status_test.go`:
- `TestClassify` — 12-case table for the (fill, shape, cleared) → Status mapping.
- `TestRecordStoresEntryById` — basic write.
- `TestRecordLastWriteWins` — newer event overwrites.
- `TestRecordClearedMarksDisconnected` — runtime "null out" event.
- `TestRecordLastErrorSurvivesRecovery` — operator-actionable: error text persists after a node comes back to green.
- `TestRecordLastErrorUpdatesOnFreshError` — only the latest error is kept.
- `TestLookupUnknownReturnsNotFound` — never-seen id.
- `TestSnapshotWholeCache` — full snapshot.
- `TestSnapshotFilterOmitsUnknown` — flow-filtered snapshot.
- `TestConsumeParsesStatusTopic` — happy parse.
- `TestConsumeIgnoresOtherTopics` — debug/hb topics don't pollute the cache.
- `TestStatusConsumeAcceptsSingleObjectFrame` — single-object envelope shape.
- `TestConsumeIgnoresBareStatusTopic` — bare "status" without id.
- `TestConsumeIgnoresUnparseablePayload` — log + skip on parse failure.
- `TestParseFlowNodeIDs` — nested v2 / flat v1 / garbage / no-id.
- `TestStatusTailRoundTripWithFakeComms` — end-to-end WebSocket.
- `TestStatusTailReconnectsAfterDrop` — server-side close is detected.
- `TestStatusTailSurvivesAnUnreachableServer` — start-up failure is contained.
- `TestStatusTailStopsWhenContextCancelled` — clean shutdown.

`internal/mcp/tools_test.go` (10 new tests):
- `TestGetRuntimeLogs_NotFoundSurfacesActionableHint` — 404 → operator hint, not generic HTTP error.
- `TestGetRuntimeLogs_ReturnsLines` — happy path: envelope → text output, levels normalised.
- `TestGetRuntimeLogs_FilterByLevel` — level filter.
- `TestGetRuntimeLogs_RejectsBadLevel` — invalid level rejected without hitting the runtime.
- `TestGetRuntimeLogs_LineOffset` — `since=-2` returns the last 2 lines.
- `TestGetRuntimeLogs_RejectsBadSince` — bad `since` rejected (5 sub-cases).
- `TestGetNodeStatus_DisabledByDefault` — `MCP_DEBUG_STREAM=off` returns clear opt-in hint.
- `TestGetNodeStatus_RequiresIdOrFlow` — neither / both rejected.
- `TestGetNodeStatus_UnknownNode` — never-seen id reported as "unknown".
- `TestGetNodeStatus_ConnectedNode` — known connected node reports `status=connected`.
- `TestGetNodeStatus_ErroredNodeWithLastError` — errored node reports `status=errored` with `last_error`; `last_error` survives a recovery to connected.

Plus the existing `TestReadOnlyExposesOnlyReadTools` and `TestFullServerExposesEveryTool` were updated to include the 2 new tools.

## Verification

```
$ go test ./... -count=1
ok    github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp      0.009s
ok    github.com/fgjcarlos/nodered-mcp/internal/config     0.002s
ok    github.com/fgjcarlos/nodered-mcp/internal/mcp        0.117s
ok    github.com/fgjcarlos/nodered-mcp/internal/nodered    0.171s
ok    github.com/fgjcarlos/nodered-mcp/internal/oauth      0.308s

$ go vet ./...
(clean)

$ gofmt -l .
(clean)
```

## End-to-end smoke test

A stock Node-RED 5.0.1 was stood up locally. The MCP was run with
`MCP_DEBUG_STREAM=true` and the `get_node_status` tool was called
both before and after triggering a `function` node that calls
`node.status({fill, shape, text})`. The results matched the audit's
acceptance criteria:

```
# before any event:
Node "n1": unknown (no status events seen on the /comms stream).
The node may be a config node, a freshly-deployed node, or an id the
model guessed. Wait a few seconds (a deploy is needed for new nodes to
start emitting status) and try again.

# after trigger of node.status({fill:"green", shape:"dot", text:"I am working"}):
Node "n1": status=connected, text="I am working" (fill=green, shape=dot), updated_at=2026-07-28T20:11:35Z

# after trigger of node.status({fill:"red", shape:"dot", text:"broken!"}):
Node "n1": status=errored, text="broken!" (fill=red, shape=dot), last_error="broken!", updated_at=2026-07-28T20:11:54Z

# flow_id variant:
Status of 2 node(s) in flow "tab1":
Tracked: 1 node id(s) known to the tail.
- Node "n1": status=errored, text="broken!" (fill=red, shape=dot), last_error="broken!", updated_at=...
- n2: unknown (no status events seen)
```

The `get_runtime_logs` call against the same Node-RED 5.0.1 returns
the expected operator hint (the endpoint is not mounted):

```
Node-RED did not respond on GET /logs. This endpoint is not part of the
standard Node-RED 5.x admin API; it may be exposed by a future version, a
fork, or a logging plugin mounted at /logs. To read the runtime log on a
stock install, point at Node-RED's stdout (the file is named
~/.node-red/*.log or the container's stdout).
```

## Caveat on tool counts

This branch is based on `main` from before PR #60 (`export_flow` /
`import_flow`) was merged. The `readOnlyTools` and `totalTools`
constants reflect the post-#51 / pre-#60 state (16 / 32). When
#60 merges, those constants will need to be bumped by one read tool
(`export_flow`).

## Out of scope (per the issue)

- Persisting logs across MCP restarts.
- Live log tailing (separate from #47).
- Changing the timeout/retry of #42/#55.
- Touching `get_debug_messages` other than the parallel-tail wiring in `startDebugTail`.

## PR

- **URL:** https://github.com/fgjcarlos/nodered-mcp/pull/61
- **Branch:** `feat/issue-51-runtime-observability` → `main`
- **Title:** `feat(mcp): get_runtime_logs + get_node_status (#51)`
- **Commit:** `fc744ff feat(mcp): get_runtime_logs + get_node_status (closes #51)`
