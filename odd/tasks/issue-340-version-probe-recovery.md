# Issue #340 — Detect local Node-RED versions and recover from failed probes

## Objective
Stop treating every loopback Node-RED URL as a test fixture, resolve the Node-RED
version before the operations that depend on it, and let a failed probe be retried
so a temporary startup failure does not leave the version unknown for the whole
session.

## Specs
S1: "Se omite el probe para cualquier URL que empiece por `http://localhost`, `http://127.0.0.1` o `http://[::1]`, tratándola como fixture. Esto incluye el valor por defecto del producto."

S2: "**Corrección:** distinguir fixtures con una opción explícita; resolver la versión antes de operaciones dependientes de ella y permitir reintento controlado tras fallos de detección."

S3: "**Aceptación:** Node-RED local se detecta; versión antigua se rechaza sin efectos secundarios; un fallo temporal al arrancar no deja la versión desconocida durante toda la sesión."

S4: "**Consecuencia:** los rechazos por versiones incompatibles no se aplican hasta que otra operación caliente la caché. Además, `sync.Once` memoriza el primer fallo del probe; si el runtime no estaba disponible al arrancar, no hay recuperación automática dentro de ese cliente."

S5: "**Evidencia:** Integración local y lectura del comportamiento de caché."

S6: "Código auditado y comprobado de nuevo antes de abrir esta issue: [main @ 93f47b1](https://github.com/fgjcarlos/nodered-mcp/commit/93f47b19a4edda1806404897df266f1a43630b11). Última release pública usada como referencia: v0.7.2."

S7: Validación de cierre: "Añadir una regresión que falle con el comportamiento actual y pase con la corrección." / "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados." / "Actualizar la documentación afectada cuando cambien garantías o límites."

S8: "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto."

## Tasks
- T1 (S1, S2): make the fixture opt-out explicit — drop the loopback URL heuristic, add an explicit skip option on the Node-RED client, probe at startup regardless of the URL.
- T2 (S2, S3, S4): resolve the version before version-dependent operations — the gate reads a probed version, not a cold cache read.
- T3 (S2, S3, S4): replace the one-shot `sync.Once` cache with a retrying cache — a failed probe is retried after a bounded interval instead of being memoized forever.
- T4 (S7): regressions that fail on the current behaviour and pass on the fix, plus updates to the tests that pinned the old heuristic.
- T5 (S7): documentation and CHANGELOG for the changed guarantee, then the scoped verification run.

## Progress

- T1 done: the loopback heuristic (`isLoopbackTestFixture`) is gone from
  `internal/mcp/server.go`; the fixture case is now the explicit
  `nodered.Options.SkipVersionProbe` switch. The startup probe runs for
  every URL, including the shipped default `http://localhost:1880`.
- T2 done: `internal/mcp/version_gate.go` gained `Server.resolveVersion`,
  which resolves the version (read-only `GET /settings`, 5s budget)
  before the operation that depends on it. `set_context` and
  `inject_node` (payload path) use it instead of reading a cold cache.
- T3 done: `internal/nodered/version.go` replaced the `sync.Once` cache
  with a mutex-guarded one that retries a failed probe after
  `versionRetryInterval` (30s). A successful probe stays cached.
- T4 done: new regressions in `internal/nodered/version_test.go` and
  `internal/mcp/version_probe_recovery_test.go`; the obsolete
  `TestIsLoopbackTestFixture` was replaced by
  `TestLoopbackURLIsNotAFixture`. Every MCP test fixture now opts out
  of the probe explicitly (`SkipVersionProbe: true`), and the
  version-sensitive ones use `serverWithMockVersionProbe`.
- T5 done: `docs/tools.md` (new "Node-RED version detection" section),
  `docs/troubleshooting.md` (the "version was not detected" entry) and
  `CHANGELOG.md` describe the new guarantee.

## Verification evidence

- RED observed on the base commit (4d78ed3) for the new regressions:
  the startup probe did not detect a loopback runtime, the two gate
  regressions were not refused, and a failed probe stayed memoized
  (`git worktree` at HEAD, ad-hoc test, worktree removed afterwards).
- GREEN after the change: `go test ./...`, `go test -race ./...`,
  `go vet ./...`, `gofmt -l .` (empty), `git diff --check` clean,
  `bash scripts/coverage-ratchet.sh` OK (86.10% vs 81.20% baseline).
- `bash scripts/complexity-check.sh` could not run: `gocyclo` is not
  installed in this environment (the script says so and exits).

## Delivery

Forecast: approximately 200-350 authored changed lines; advisory
estimate, not a hard cap. Actual: 909 authored changed lines in one
commit (`fix(mcp): detect local Node-RED versions and recover from
failed probes (#340)`), which is over the ~400-line reviewable slice.

The size is not optional padding: about 60 of the lines are the same
one-line change in the MCP test fixtures (`SkipVersionProbe: true` on
the client). They cannot be a separate commit, because without them
the suite fails the moment the production code starts probing a
fixture — the opt-out and the behaviour it guards are one unit.

Strategy: single commit, single PR. The native review was assessed
(`gentle_review assess`, base `origin/main`) as medium risk with
`reviewDue: false` ("under_budget"), so no review lineage was started.

## Log
- L1 (2026-10-19, user): "Resuelve la issue #340" — issue #340 "[P2][A13] detect local Node-RED versions and recover from failed probes" (audit-2026-10-19 · A13 · P2), labels bug/qa. Working tree clean at 4d78ed3.
- L2: Design decisions taken from the issue text: fixtures are distinguished by an explicit option (not by URL shape); the version is resolved inside the gate before any dependent operation; a failed probe is retried after a bounded interval.
- L3: Engram mirror pending: no Engram/memory tools are available in this runtime's tool catalog.
