# Issue #347 — cancelable write-lock waits

Make the mutation lock waits cancelable so a request whose context is done gives up instead of blocking past its deadline.

## Specs

S1. "Los handlers llaman a `sync.Mutex.Lock` sin vincular la espera al contexto. El wrapper llama al handler de manera síncrona, por lo que el deadline no lo interrumpe mientras espera el mutex. Una cola de escrituras puede exceder el timeout anunciado y mantener peticiones ya canceladas esperando."

S2. "**Corrección/aceptación:** adquisición cancelable o cola acotada, y prueba en la que una segunda escritura con contexto expirado abandona mientras otra retiene el guard. No basta con verificar el deadline de las llamadas HTTP posteriores."

S3. "**Código:** [internal/nodered/client.go:writeGuard](https://github.com/fgjcarlos/nodered-mcp/blob/93f47b19a4edda1806404897df266f1a43630b11/internal/nodered/client.go), [internal/mcp/tools_context.go:ctxHelperMu.Lock](https://github.com/fgjcarlos/nodered-mcp/blob/93f47b19a4edda1806404897df266f1a43630b11/internal/mcp/tools_context.go) y restauración."

S4. "## Validación de cierre
- [ ] Añadir una regresión que falle con el comportamiento actual y pase con la corrección.
- [ ] Ejecutar las comprobaciones adecuadas al alcance y registrar resultados.
- [ ] Actualizar la documentación afectada cuando cambien garantías o límites."

S5. "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto."

S6. "**Pendiente de validación dinámica:** este hallazgo procede de inspección; no debe presentarse como una reproducción de extremo a extremo."

## Tasks

T1 | S1-S2 | inline (RED test at the client layer) | done | `internal/nodered/write_lock_test.go`: a second write with a canceled context and one with an expired deadline abandon the wait while another write holds the guard. RED on the base: both waited past their deadline (2s timeout hit) and the canceled write reached the runtime.
T2 | S2-S3 | inline (cancelable acquisition) | done | `internal/lock.CtxMutex` (shared), `internal/mcp.helperMutex` is now an alias of it, `Client.writeGuard(ctx)` returns `(release, error)` and all 14 mutating entry points acquire with the caller's context.
T3 | S3 | inline (already-fixed half verified) | done | `ctxHelperMu` and the restore path already use `LockContext(ctx)`; unchanged, and `internal/mcp/setcontext_review_test.go` still pins the helper-lock refusal.
T4 | S4 | inline (docs + checks) | done | docs/tools.md names both refusals; docs/architecture.md states that both waits honor the caller's context. Full suite, vet, race, coverage ratchet (86.10% >= 81.20%), complexity gate, gofmt and `git diff --check` all pass.
T5 | S4 | independent verification (concurrency risk) | done | `gentle-ai-verify` on the diff (task mwjkqukd-2-2afj): no blocker, three findings (F1 indeterminate-outcome classification, F2 error precedence, F3 double unlock); F1 and F3 corrected, F2 accepted and documented.

## Log

L1. User request verbatim: "Resuelve la issue #347"

L2. Issue #347 (bug, qa) is open; it audits commit 93f47b1 and names `internal/nodered/client.go:writeGuard`, `internal/mcp/tools_context.go:ctxHelperMu.Lock` "y restauración". It states the finding comes from inspection, not from an end-to-end reproduction, and points at #42 and #310 as related work without redoing it.

L3. Current tree: the MCP half is already fixed. `Server.ctxHelperMu` is a `helperMutex` whose `LockContext(ctx)` abandons a canceled/deadline waiter (internal/mcp/context_helper_mutex.go), taken by `addWriteTool` (server.go:366), by `dispatchSetContext` (tools_context.go:142) and by `handleRestoreBackup` (tools_backups.go:162). `internal/mcp/setcontext_review_test.go` already pins the refusal text "waiting for context helper lock: context canceled; no action was started". The client half is not fixed: `Client.writeMu` is a plain `sync.Mutex` and `writeGuard()` blocks uninterruptibly on all 14 mutating entry points.

L4. Scope decision: fix the client write lock with a cancelable acquisition, reusing the existing helperMutex pattern instead of a second primitive. Do not re-implement the MCP half, and do not touch #42/#310 work. Risk: item 4 (concurrency) — the lock's acquisition semantics change.

L5. Planned shape: extract the context-aware mutex into a shared internal package so the MCP layer and the client use one implementation (`internal/lock.CtxMutex`, `internal/mcp.helperMutex` becomes an alias). `Client.writeGuard(ctx)` returns `(release func(), error)`; every mutating entry point acquires with the caller's context and returns the refusal "waiting for write lock: ...; no action was started" when the context is done. The MCP refusal text for the helper lock stays untouched.

L6. RED observed on the base: `go test ./internal/nodered -run 'TestWriteLock|TestWriteGuard' -count=1` failed — the second write waited 2s past its deadline in both the canceled and the expired case, and the canceled write reached the runtime (its snapshot failed with "context canceled" instead of a refusal).

L7. Implementation: `internal/lock.CtxMutex` is the one context-aware mutex; `internal/mcp.helperMutex` is now an alias of it, so the helper lock and the client write lock share the implementation and its behavior is unchanged. `writeGuard(ctx)` returns the release function or the refusal "waiting for write lock: <cause>; no action was started"; the 14 mutating entry points acquire before they validate, snapshot or contact the runtime. The refusal is exact, not a wrapped generic error.

L8. GREEN: focused regressions exit 0; `go test ./...` exit 0; `go test -race ./internal/nodered ./internal/mcp ./internal/lock` exit 0; `go vet ./...` exit 0; gofmt clean; `git diff --check` clean; coverage ratchet 86.10% against a floor of 81.20%.

L9. Complexity gate: the cancelable acquisition adds one branch to every mutating method, so `setFlows` (8 -> 9) and `RestoreFlows` (6 -> 7) — the two named in scripts/complexity-baseline.txt — moved by one. The bump is a deliberate, documented entry in that file: no floor is lowered and the threshold of 15 is untouched. The alternative (refactoring unrelated validation to keep a number down) was rejected as out of scope.

L10. Known limits: the locks are process-local, so two MCP servers in one process that share a client are serialized by the client lock but not by the helper lock; external processes are not synchronized at all. The MCP half of the finding was already fixed on main, so this change adds the client half only; it does not make the helper lock cover anything it did not cover before.

L11. Independent verification (`gentle-ai-verify`, task mwjkqukd-2-2afj, risk item 4) found no blocker: every mutating entry point takes the lock exactly once, a refused acquisition reaches neither the runtime nor the backup folder, the token accounting survives 64 goroutines with random cancellations under `-race`, and there is no lock-order inversion between `ctxHelperMu` and the client write lock. Three findings came out of it:

- F1 (follow-up, corrected): through MCP, a refused client-lock wait was reported as "outcome is indeterminate ... The action may already have executed", because `isTransportError` treats any wrapped context error as a transport failure. That contradicts the refusal the docs promise.
- F2 (observation, accepted): with a canceled context the lock refusal now precedes the argument validation error (`DeleteFlow(ctx, "")`, `InstallNode(ctx, "", "")`, `SetFlowsState(ctx, "bogus")`). The MCP handlers validate their arguments before they call the client, so no tool sees it. A canceled request is refused before it is validated; that is the truthful order.
- F3 (observation, corrected): `Unlock` on a lock nobody holds used to block; `sync.Mutex` panics. The client lock used to be a `sync.Mutex`, so `Unlock` now panics instead of hanging.

L12. Corrections: `nodered.WriteLockRefusedError` (typed, keeps `Unwrap`) is what `writeGuard` returns; `isTransportError` recognizes it before the context errors, so the refusal is reported as a refusal and not as an indeterminate outcome; `internal/lock`'s `Unlock` panics on a double unlock. The new regression `internal/mcp/write_lock_refusal_test.go` holds the client write lock with a direct client call and checks that the tool reports the refusal. RED without the classification fix ("outcome is indeterminate: calling Node-RED: waiting for write lock: ..."), GREEN with it.

L13. Delivery shape: the implementation slice is 509 authored changed lines (452 added, 57 removed), over the ~400-line advisory slice; 228 of those lines are the two regressions. The branch carries one PR: the behavior (cancelable write waits) and the corrections from its verification are one reviewable unit, and the second commit is small and confined to the findings above.
