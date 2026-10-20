# Issue 328 — Avoid ambiguous write replay
Branch: `fix/issue-328-safe-write-retries`; base: `93f47b19a4edda1806404897df266f1a43630b11`; delivery: auto-chain; chain_strategy: feature-branch-chain (user-selected); updated forecast ~600 authored lines; publication policy requires one PR per issue.
Runner: Go tests, vet, formatting and repository complexity check. Engram mirror unavailable (no callable memory tools).

## Specs
S1. User authorization verbatim: “Resuelve la issue #328”. Issue correction verbatim: “reintentos automáticos solo en lecturas y operaciones con idempotencia demostrada.”
S2. Issue acceptance verbatim: “ante un cierre después de recibir el POST, una acción sin idempotencia envía exactamente un POST y comunica incertidumbre; las lecturas mantienen sus reintentos.” Reproduction verbatim: “un fixture recibe `POST /inject/i` y cierra la conexión sin respuesta. Una sola llamada MCP a `inject_node` produce **tres POST**.”
S3. Issue correction verbatim: “Para escrituras con respuesta incierta, devolver estado indeterminado y recomendar consultar el runtime antes de repetir. Evitar clasificar por fragmentos de texto como `EOF`.”
S4. Closure validation verbatim: “Añadir una regresión que falle con el comportamiento actual y pase con la corrección.”; “Ejecutar las comprobaciones adecuadas al alcance y registrar resultados.”; “Actualizar la documentación afectada cuando cambien garantías o límites.”

## Tasks
T1 | S1–S4 | inline recovery (explicit user authorization) | done | public RED/GREEN observed; full Go suite + vet PASS; formatting/diff PASS; 91-scenario public differential matches except 44 intentional text-classification fixes | commit: 505627c, a3d84cb, 0a03c58
T2 | S1–S4 | independent verifier + parent spot check | done | gentle-ai-verify verdict S1–S4 met, no blockers; independent public stdio probes confirm one POST plus uncertainty, read retries, marker-free classification; palette regression committed; parent spot check PASS | commit: a3d84cb
T3 | S1–S4 | inline | done | slice boundaries recorded, three coherent work-unit commits, result reported | commit: 505627c, a3d84cb, 0a03c58

Slice boundaries (feature-branch chain, user-selected):
- Slice A 505627c refactor(nodered): mark failed HTTP exchanges as transport errors — 68 authored lines, baseline-safe, verified green in an isolated copy of the base plus slice A.
- Slice B a3d84cb fix(mcp): stop replaying mutating tools after ambiguous transport failures — 504 authored lines, behavior plus its regressions, the indivisible unit.
- Slice C 0a03c58 docs: describe the read retry and indeterminate write guarantees — 46 authored lines.
Total 618 authored changed lines excluding this record. Slice B exceeds the ~400 heuristic because behavior and its regressions travel together; no coherent sub-400 split exists that keeps them together.


## Log
L11. Independent verification (gentle-ai-verify, mission 479ff1b4-577a-4d47-ad9f-79b04cc31b23) returned verdict met for S1–S4 with no blockers: public stdio MCP probes against localhost fixtures reproduced accepted POST plus closed connection (exactly one POST, indeterminate guidance), read recovery on attempt three for disconnect and truncated bodies, exhaustion at three, marker-bearing HTTP 400 answered once without indeterminate classification, and zero HTTP requests for validation boundaries. Verifier proposed one durable palette regression; parent added it as TestIssue328PaletteWriteResponses in internal/mcp/retry_test.go and observed it PASS. Verifier reused parent full-suite evidence instead of rerunning it; scratch /tmp/issue-328-verify.2q5KnF left for parent cleanup. Parent then found that internal/mcp/clipboard.go (export_flow, import_flow) still used the plain result adapter and would have kept marker-text classification; parent adapted both handlers to toolResultError, re-ran gofmt/vet/full suite/diff check PASS (job-4, ALL_CHECKS_PASS), and re-ran the public differential with import_flow and export_flow coverage. Remaining unavailable proof: live Node-RED runtime, remote CI, and the complexity gate (gocyclo absent, no install attempted).
L12. Work-unit commits created on fix/issue-328-safe-write-retries: A 505627c, B a3d84cb, C 0a03c58. Chain strategy kept the repository's one-branch-per-issue naming and the coherent commit boundaries; the tracked feature-branch chain holds these commits as its slices. Complexity gate run with the pinned gocyclo (GOPATH exported): exit 0, handleGetRuntimeLogs 14/14, handleSetContext 10/10, normalizeFlowDoc 6/6, ValidateFlow 10/10, synthesizeFlowFromFlat 15 (baseline 16, improved). README.md and README.es.md synchronized with the same guarantee paragraph. Public differential re-run with import_flow and export_flow coverage: 91 scenarios, 46 identical, 45 intended marker-error changes, no unexpected difference; import_flow 3 -> 1 request and export_flow 3 -> 1 request under marker-bearing API errors.
L13. Publication: PR #352 (https://github.com/fgjcarlos/nodered-mcp/pull/352, base main, head fix/issue-328-transport-errors, 68 authored lines, type:chore + go) and PR #353 (https://github.com/fgjcarlos/nodered-mcp/pull/353, base fix/issue-328-transport-errors, head fix/issue-328-safe-write-retries, 621 authored lines, type:bug + go). Both carry Refs #324-style nonclosing linkage to #328; no issue status label applied because no authorization covered it; no merge performed. CI for both PRs pending at the time of this entry. Repository policy expects one branch per issue, while the user-selected chain strategy needs a second branch; the split is two PRs for one issue, disclosed in both bodies.
L14. Remote CI observed green on both PRs: run 39324394983 for #352 and run 39324516892 for #353, each with check, test on ubuntu/macos/windows, race, npm-wrapper on ubuntu/windows, docker-smoke, cross-build, release-snapshot and release-artifact-smoke on ubuntu/windows/macos, plus GitGuardian. No failing or skipped check. Issue #328 remains open pending human review and merge; no status label was added because no authorization covered it.
L1. User request verbatim: “Resuelve la issue #328”.
L10. Parent implementation complete. Focused `go test ./internal/mcp -run 'Retry|Transport|Ambiguous' -count=1` and registry typed-error regression PASS. Baseline scratch new-rule regressions exited 1 as expected (three POST, false marker retries, deadline/cancellation triple replay); registry typed-error regression RED then GREEN. Full `go test ./...` and `go vet ./...` PASS (job-1); complexity checker exited 2 because gocyclo is unavailable, not installed or retried. Formatting and git diff --check PASS separately after that early stop. Initial extra test assertions were corrected to preserve existing inject behavior (no configuration backup; normal metadata/lookup reads). No production change was made to those behaviors. Public differential: 91 scenarios across all 44 registered tools and three representative successes; 47 identical and 44 intended marker-error changes, no unrequested difference after normalizing only ephemeral fixture ports. Initial comparator flagged four random-port differences; corrected comparison only, no checks rerun. Logs/full protocol JSON/counters retained in /tmp/issue-328-differential.QIMjLs; both probe commands exited 0, stderr empty. ASSESS risk high via item 1; native assess unassessable due untracked declaration, RDD off; independent verifier required. Docs updated in architecture/tools/CHANGELOG; READMEs unchanged.
L9. User delivery strategy selection verbatim: “Cadena sobre rama de feature”. Keep coherent verified work-unit commits on feature branch; no publication is implied. Repository policy still requires one PR per issue; reconcile any future publication before creating PRs. Updated forecast ~600 lines including typed error adaptation and public compatibility regressions; avoid artificial code/test splitting.
L8. User recovery authorization verbatim: “Sí, terminar en esta sesión (recomendado)”. Parent completes existing partial implementation inline, no worker restart; independent verification remains required.
L7. Standard worker timed out after 1800000ms while its additional-surface supervisor request was pending; no restart attempted. Inspected partial diff: 9 tracked source files modified plus untracked retry_test.go and errors.go, no commits. Observed RED command `go test ./internal/mcp -run '^TestInjectNodeAmbiguousTransportDoesNotRetry$' -count=1` exited 1: POST /inject/i count = 3, want 1; MCP JSON-RPC error instead of tool error result. GREEN, existing tests adaptation, registry path, docs, full checks and independent verification remain incomplete. Need user recovery-mode authorization, not more verification of unfinished code.
L6. User edit-surface authorization verbatim: “Sí, incluir ambos archivos (recomendado)” for internal/nodered/settings.go and internal/nodered/settings_test.go to retain search_nodes registry read transport retries with typed errors. Parent owns long/unknown-duration checks because worker lacks bash_background.
L5. User fallback authorization verbatim: “Sí, usar worker estándar”. Keep scope and verification unchanged, no environment configuration change.
L4. Worker launch failed before producing output: foreground gentle-ai-worker requires unavailable mem_save. Mission c1832dd7-0b9c-4b2c-b631-30390f465be4. No source edit or test execution occurred. No retry or execution-mode fallback attempted. Need user authorization for an alternative worker profile without that extension dependency; implementation and required independent verification remain pending.
L2. Issue source: https://github.com/fgjcarlos/nodered-mcp/issues/328. Verbatim issue body follows:

## Referencia de auditoría

**audit-2026-10-19 · A01 · P1**

Código auditado y comprobado de nuevo antes de abrir esta issue: [main @ 93f47b1](https://github.com/fgjcarlos/nodered-mcp/commit/93f47b19a4edda1806404897df266f1a43630b11). Última release pública usada como referencia: v0.7.2.

**Evidencia:** Fixture HTTP; tres POST para una llamada MCP.

## Hallazgo

**Código:** [internal/mcp/server.go](https://github.com/fgjcarlos/nodered-mcp/blob/93f47b19a4edda1806404897df266f1a43630b11/internal/mcp/server.go), `addWriteTool`, `withTimeoutRetry`, `resultIsTransportError`.

Las herramientas de lectura y escritura comparten hasta tres intentos. Una conexión cerrada después de que Node-RED reciba una acción no permite saber si esta se ejecutó. El wrapper vuelve a lanzarla igualmente.

**Reproducción:** un fixture recibe `POST /inject/i` y cierra la conexión sin respuesta. Una sola llamada MCP a `inject_node` produce **tres POST**. El fixture demuestra la duplicación de envíos; no pretende que un cierre de conexión revierta una acción ya ejecutada.

**Impacto:** duplicación de mensajes o comandos a hardware, pestañas repetidas y operaciones sobre la paleta repetidas. Un error final no implica que la acción no se haya producido.

**Corrección:** reintentos automáticos solo en lecturas y operaciones con idempotencia demostrada. Para escrituras con respuesta incierta, devolver estado indeterminado y recomendar consultar el runtime antes de repetir. Evitar clasificar por fragmentos de texto como `EOF`.

**Aceptación:** ante un cierre después de recibir el POST, una acción sin idempotencia envía exactamente un POST y comunica incertidumbre; las lecturas mantienen sus reintentos.

## Trabajo relacionado

- #42

Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto.

## Validación de cierre

- [ ] Añadir una regresión que falle con el comportamiento actual y pase con la corrección.
- [ ] Ejecutar las comprobaciones adecuadas al alcance y registrar resultados.
- [ ] Actualizar la documentación afectada cuando cambien garantías o límites.


L3. Clean starting tree; fetched origin/main and created an issue-specific branch at 93f47b1. No deployment, release, merge or live Node-RED mutation is in scope. Preserve backups, guards, timeout bounds and existing non-transport error handling. Risk: item 1 (replay may repeat irreversible runtime actions); shared retry/error adaptation also needs compatibility checks. Parent context backstop requires bounded writer. No Engram tools callable; local file is authoritative pending mirror availability.
