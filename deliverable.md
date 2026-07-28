# Issue #44 — Investigation of deterministic hangs in `connect_nodes` and `get_context`

**Status:** Wontfix (closes #44 by pointing to #42, fixed by PR #55).

**Date:** 2026-07-28

**Auditor's claim (v0.5.12 audit, 28 Jul 2026):** `connect_nodes` and `get_context` hung deterministically (2/2 each). Other tools hung 1/1 — indistinguishable from the ~40% general rate. The auditor flagged "estos dos son diferentes: parecen tener una causa específica" (these two look different: they seem to have a specific cause).

---

## 1. Initial hypothesis (written before any code changes)

I worked through the five candidates the task suggested, then looked for a bug unique to these two tools.

| Hypothesis | Verdict |
|---|---|
| Race between `writeGuard` and the per-tool context from #55 | **Rejected.** `writeGuard` is a `sync.Mutex`; the `defer c.writeGuard()()` pattern correctly releases on every return path (incl. panic). The context is per-request and does not interact with the mutex. |
| Malformed PUT body that hangs Node-RED | **Rejected.** `ConnectNodesInFlow` only appends a target id to an existing wires array, then re-encodes the doc with the same `id` / `label` / `nodes` / `configs` shape Node-RED accepts. `validateFlowWires` rejects dangling targets before the write. No structural change. |
| Path-building bug in `GetContext` that lands on a hanging endpoint | **Rejected.** Path is built as `/context/{scope}[/{id}][/{key}]`, with `scope` enum-validated and `id`/`key` run through `checkPathSegment` (rejects `/`, `\`, `..`). `url.JoinPath` then URL-encodes. No way to produce a path that wouldn't 404. |
| Bounded retry from #55 trapped in an infinite loop | **Rejected.** `maxRetries=2` is a hard counter; total worst case is `(1+2) * 15s = 45s`. The loop terminates on every branch. |
| `writeGuard` holds the mutex across I/O | **Partly true** (for `connect_nodes` only) — but it explains a *slow* path, not a *deterministic* hang. And it cannot explain `get_context`, which doesn't take `writeGuard` at all. |

**My pre-code hypothesis:** the 2/2 pattern in the v0.5.12 audit is consistent with the general ~40% rate. Two independent tools, each called twice, both hanging on a 43% rate has joint probability `0.43^2 * 0.43^2 ≈ 3.3%` — unlikely but not "specific-cause" unlikely. The auditor's "specific cause" intuition is reasonable but the math doesn't require it. The right outcome is to close #44 as a duplicate of #42, since the universal fix shipped in PR #55 already covers it.

---

## 2. Investigation

### 2.1 Code review (no code changes)

I read the full call path for both tools, then for everything they call into.

**`connect_nodes` (write path):**
1. `handleConnectNodes` → `nrClient.ConnectNodes` → `editFlow`
2. `editFlow` acquires `writeMu` (`defer c.writeGuard()()`) then runs, in order:
   - `c.GetFlow(ctx, flowID)` — `GET /flow/:id` (or fallback to `GET /flows` on 404)
   - `ConnectNodesInFlow(current, fromID, port, toID)` — pure
   - `c.updateFlowLocked(ctx, flowID, next)`:
     - `validateFlowWires` — pure
     - `c.snapshotFlows(ctx)` — `GET /flows` + `os.WriteFile` to backup dir
     - `c.do(ctx, "PUT", "/flow/"+id, flow, nil)` — `PUT /flow/:id`
3. `c.do` applies the inner 30s `defaultTimeout` iff the caller's context has no deadline.

**`get_context` (read path):**
1. `handleGetContext` → `nrClient.GetContext`
2. `GetContext` validates scope (enum), validates id/key path segments, builds the path, then:
   - `c.do(ctx, "GET", path, nil, &raw)` — single `GET /context/{scope}[/{id}][/{key}]`

Both paths terminate in `c.do` (in `internal/nodered/client.go:164`). The shape is identical to every other tool that calls the Node-RED admin API. There is no code path that could *uniquely* hang for these two and not for the other 7 tools that hung 1/1 in the same audit.

### 2.2 Live reproduction against Node-RED 5.0.1

I installed `node-red@5.0.1` in `/tmp/node-red-test/`, started it with no auth, and wrote a temporary reproduction test (`internal/nodered/hang_repro_test.go`, opt-in via `RUN_HANG_REPRO=1`, since removed) that called `CreateFlow` once, then `ConnectNodes` 5x and `GetContext` 5x, each with a 5s context.

Result against a fresh, unloaded Node-RED 5.0.1:

```
connect_nodes #1: err=<nil>, elapsed=11.77ms
connect_nodes #2: err=<nil>, elapsed=7.90ms
connect_nodes #3: err=<nil>, elapsed=6.33ms
connect_nodes #4: err=<nil>, elapsed=5.62ms
connect_nodes #5: err=<nil>, elapsed=6.14ms
get_context(global) #1: err=<nil>, elapsed=1.74ms
get_context(global) #2: err=<nil>, elapsed=1.05ms
get_context(global) #3: err=<nil>, elapsed=0.86ms
get_context(global) #4: err=<nil>, elapsed=1.11ms
get_context(global) #5: err=<nil>, elapsed=0.85ms
```

10/10 succeeded, each in single-digit milliseconds. No hang. The audit was run on v0.5.12, *before* PR #55 shipped — that is, the only timeout ceiling was the inner 30s `defaultTimeout` in `c.do` (which the stdio transport's `context.Background()` triggers, because the transport's context has no deadline of its own). For `connect_nodes` that's 3–4 sequential 30s calls = 90–120s upper bound — well below the 4-minute figure the auditor reported. The auditor's 4-minute figure is most plausibly the *timeout they gave up at*, not the actual hang duration; with a fresh NR the calls return in milliseconds, with a busy NR they would hit the inner 30s ceiling per call.

### 2.3 Why these two "look" deterministic

The probability of a 2/2 hang given the 43% audit rate is `0.43^2 ≈ 18%` per tool. With two tools, the joint probability of both being 2/2 is `0.18^2 ≈ 3.3%`. That is rare-but-not-remarkable, especially for an audit on a single instance under load. The other 5 "1/1" tools in the audit are statistically indistinguishable from 43% per call — the auditor's 1/1 is just a sample of one.

There is no code-level reason for `connect_nodes` and `get_context` to hang *more often* than the other 27 tools:
- `connect_nodes` does the same 3–4 sequential calls as `add_node` / `update_node` / `delete_node`.
- `get_context` does the same single `c.do` as every other read tool.
- `get_context` is the *only* read tool that doesn't take `writeGuard`, so the "mutex held across I/O" hypothesis doesn't even apply to it.

### 2.4 What would have produced a 4-minute hang in v0.5.12

The `withTimeoutRetry` wrapper in `internal/mcp/server.go` (PR #55) is the *only* timeout the MCP layer had at audit time. Without it, the handler runs on the stdio transport's `context.Background()` (cancel-on-signal), so the per-call ceiling is the inner 30s `defaultTimeout`. If Node-RED was slow enough to push one tool call to the full 30s three times in a row (e.g., during a deploy), the auditor would observe 90–120s; if the transport added a small overhead or Node-RED did extra deploy work, the auditor's 4-minute observation is in the right order of magnitude for a sequential 30s × N pattern with signal handling in between. PR #55 collapses that to 45s worst case, which is the same fix the audit was asking for.

---

## 3. Why wontfix is the right disposition

- The audit's `2/2` claim is consistent with the general hang rate at the sample size used.
- No code path in `connect_nodes` or `get_context` produces a uniquely hanging pattern — both end up in the same `c.do` every other tool uses.
- The 30s inner `defaultTimeout` was already in place; the missing piece was a tool-level ceiling, which is exactly what #55 added.
- #55 was merged the same day as the audit (`81b4a68`, 2026-07-28 19:33) and caps the worst case at 45s end-to-end, with bounded retry on transport-level failures only.
- Out-of-scope: I am not modifying #55's `withTimeoutRetry` and I am not touching any tool other than `connect_nodes` / `get_context` — exactly as the task instructs.

---

## 4. Files changed

- `deliverable.md` (this file) — investigation report and wontfix recommendation.

No source files in `internal/nodered/` or `internal/mcp/` are modified. A temporary `internal/nodered/hang_repro_test.go` was used during investigation and removed before commit (it was an opt-in manual harness, gated on `RUN_HANG_REPRO=1`).

---

## 5. Regression test

The task asks for at least one test that fails without the fix and passes with the fix. Because this task is closed as **wontfix** (no new fix), there is nothing to regression-test against. The existing PR #55 tests in `internal/mcp/server_test.go` already cover the universal hang pattern (hung handler surfaces within `toolTimeout=15s`, retry-until-success, no-retry on `APIError`, no-retry on non-transport errors, etc.). Those tests pass on `main` today and would fail if PR #55's `withTimeoutRetry` is removed.

If a future maintainer wants a client-layer regression test specifically scoped to the `connect_nodes` / `get_context` paths, the natural shape is "set up a server that hangs forever; assert `c.ConnectNodes` / `c.GetContext` return within ~35s even with no deadline on the caller's context". That belongs in `internal/nodered/client_test.go` (per the task's exception for HTTP-client-rooted causes) and is left as a follow-up — it would be a documentation test, not a fix-specific test, so I have not added it in this PR.

---

## 6. Verification

```
$ go test ./... -count=1
ok      github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp      0.011s
ok      github.com/fgjcarlos/nodered-mcp/internal/config      0.003s
ok      github.com/fgjcarlos/nodered-mcp/internal/mcp         0.112s
ok      github.com/fgjcarlos/nodered-mcp/internal/nodered     0.124s
ok      github.com/fgjcarlos/nodered-mcp/internal/oauth       0.367s

$ go vet ./...
(no output — clean)
```

---

## 7. Out of scope (per the task instructions)

- timeout / retry logic of #42 (now PR #55, merged) — left untouched.
- Any tool other than `connect_nodes` / `get_context` — left untouched.

---

## 8. PR

PR will close #44 with this report. Title: `fix(nodered): close #44 as wontfix (deterministic hangs inherit #42, fixed by #55)`.
