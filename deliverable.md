# Issue #51 — runtime log read + live node status

**Status:** Implementation complete. PR opened.

**Date:** 2026-07-28

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
| `internal/nodered/logs_test.go` | **NEW** — 4 tests: stock-Node-RED 404, envelope shape, bare array, plain text |
| `internal/nodered/status.go` | **NEW** — `StatusTail`, `NewStatusTail`, `Run`, `Lookup`, `Snapshot`, `ParseFlowNodeIDs` |
| `internal/nodered/status_test.go` | **NEW** — 7 tests for the StatusTail lifecycle and event handling |
| `internal/nodered/client.go` | Added `getRaw` helper for endpoints whose body may not be JSON-decodable |
| `internal/mcp/server.go` | Wired `StatusTail` into the Server; `Run` is launched on the same goroutine as `DebugTail` |
| `internal/mcp/tools.go` | Registered `get_runtime_logs` (read) and `get_node_status` (read) via `addReadTool` |
| `internal/mcp/tools_test.go` | Added the 2 new tools to `readOnlyTools`; updated `totalTools` to 32 |

## Tests added

- `TestGetRuntimeLogs_*` (4 tests in `logs_test.go`) — stock 404,
  envelope, bare array, plain text.
- `TestStatusTail_*` (7 tests in `status_test.go`) — coverage of the
  cache lifecycle, event parsing, and the flow-id expansion helper.
- The existing `TestReadOnlyExposesOnlyReadTools` and
  `TestFullServerExposesEveryTool` exercise that the 2 new tools
  appear in the right surface.

## Verification

```
$ go test ./... -count=1
ok    github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp      0.009s
ok    github.com/fgjcarlos/nodered-mcp/internal/config     0.002s
ok    github.com/fgjcarlos/nodered-mcp/internal/mcp        0.114s
ok    github.com/fgjcarlos/nodered-mcp/internal/nodered    0.162s
ok    github.com/fgjcarlos/nodered-mcp/internal/oauth      0.251s

$ go vet ./...
(no output — clean)

$ gofmt -l .
(no output — clean)
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

## PR

- **URL:** <filled in by the committer>
- **Branch:** `feat/issue-51-runtime-observability` → `main`
- **Title:** `feat(mcp): get_runtime_logs + get_node_status (closes #51)`
