# Issue #344 — retry stale npm values until the expected value is visible

## Specs

- S1. "El helper acepta el primer stdout no vacío. Después de promover un dist-tag, el registro puede responder con la versión anterior, que también es no vacía. El helper devuelve éxito inmediatamente y la comparación externa falla sin reintentar." (issue #344)
- S2. "Reproducción: comando simulado que devuelve primero `0.7.2` y después `0.7.3`; el helper devuelve `0.7.2` tras **una sola lectura**, con exit 0. La segunda respuesta nunca se consulta." (issue #344)
- S3. "Corrección: añadir comparación/predicado del valor esperado dentro del bucle; preservar el error real del comando cuando exista." (issue #344)
- S4. "Aceptación: antiguo → esperado reintenta y pasa; antiguo permanente agota el presupuesto y falla; vacío → esperado sigue funcionando." (issue #344)
- S5. "Añadir una regresión que falle con el comportamiento actual y pase con la corrección." (issue #344, validación de cierre)
- S6. "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados." (issue #344, validación de cierre)
- S7. "Actualizar la documentación afectada cuando cambien garantías o límites." (issue #344, validación de cierre)
- S8. "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto." (issue #344)

## Tasks

- T1 (S1, S2) — Localizar el helper, sus cuatro call sites y la regresión existente. Route: read. Commit: —
- T2 (S4, S5) — Regresiones del helper: valor obsoleto → esperado, valor obsoleto permanente, vacío → esperado, error real del comando. Route: inline. Commit: —
- T3 (S3, S4) — `--expect` dentro del bucle y captura del stderr del comando. Route: inline. Commit: —
- T4 (S4) — Cablear `--expect` en la verificación de dist-tags y pinnearlo en el guard del workflow. Route: inline. Commit: —
- T5 (S6, S7) — Comprobaciones y documentación. Route: inline. Commit: —

## Delivery

- Commit: `0538c83` — `fix(release): retry stale npm values until the expected value is visible (#344)`
  (215 authored changed lines, one work-unit commit on
  `fix/issue-344-registry-expected-value`).
- Pull request: #380, against `main`, with `Closes #344`.

Checks (all green):

- `node bin/*_test.js` (the whole `bin/` suite): PASS
- `go test ./...`: PASS; `go vet ./...`: clean; `gofmt -l .`: clean
- `bash scripts/coverage-ratchet.sh` → 86.20% (baseline 81.20%)
- `git diff --check`: clean; no CRLF in the touched text sources
- Differential check of the helper (old vs. new, in a `mktemp -d` scratch
  directory, never committed): for the three call sites that do **not**
  use `--expect` (`npm view <pkg>@<version> dist.integrity`), stdout,
  stderr and exit code are identical.

No Go code changed, so `go test -race ./...` and the complexity check add
no signal for this diff; CI runs them anyway.

### Native review

Lineage `review-56d705c5ba7fb125`, tier high, lenses resilience +
reliability, candidate `d108a5c2…` (the final tree, commit `654f96a`).
The two materialize reviewer runs completed (`minimax/MiniMax-M3.1-Flash`,
2 runs, 32.5 KB prompts) and the lineage closed **approved**; the exact
`acknowledge-approved` binding burned the authority
(`gentle-ai.review-acknowledged/v1`, delivery under ordinary repository
policy).

One advisory finding, `R3-EXPECT-MISSING-VALUE` (reliability, WARNING,
informational), at `scripts/wait-for-registry.sh:58`: `--expect` without
a value makes `"$2"` unbound, so `set -u` aborts with exit 1 instead of
the documented exit 2 for an argument error. The pre-existing
`--attempts` and `--base-sleep` flags behave the same way, so the
helper is consistent; the closure is explicit that this finding is
separate later work and never a reason to re-run the review on this
candidate.

Three earlier lineages were opened and abandoned with a committed
`review-reclaim-record/v1` (`operator_disposition`, actor `fgjcarlos`):
`review-bd0ec40c64b8b910` (work-unit commit `0538c83`),
`review-562246593b41056b` (candidate `9642e94`) and
`review-56f3a88b9b68d99e` (candidate `67b8877`). The capture was
blocked in all three, and **both causes were in the calling session,
not in the harness**:

1. **Selector mismatch.** The capture path re-registers its retained
   route with the selector derived from the candidate projection
   (`providerBaseTree ?? baseCommit`), so the STATUS read and the capture
   must use the same one. Reading STATUS with the provider base tree
   (`e4aa39b4…`) or with a symbolic ref (`origin/main`) collides with
   that selector and the capture is rejected with
   `capture-route-registration-rejected` / `selector-mismatch`. Reading
   STATUS with the base **commit** (`7dd195f9…`, the same selector the
   START used) is the selector that matches.
2. **A mistranscribed binding.** The binding read from STATUS carries
   `artifactSubject.schema = "gentle-ai.review-artifact-subject/v2"`. The
   binding submitted to the capture carried
   `"gentle-ai.dev/review-artifact-subject/v2"` — one token off. Compared
   programmatically against the session transcript (no hand
   transcription), that single field was the *only* difference in every
   lineage, which is why the capture reported
   `collectBinding is missing or stale for current STATUS` and
   `collectBindings must be the complete distinct current reviewer group
   in exact provider order`.

Recipe for the next run: read STATUS with the same base commit the START
used, and submit the binding bytes exactly as STATUS returned them —
verified against the session transcript before sending, not retyped.

## Log

- L1: Petición original: "Resuelve la issue #344" (2026-11-16).
- L2: El alcance real es el helper `scripts/wait-for-registry.sh`, creado en
  `fix/issue-298-npm-registry-retry` (#298) para tolerar la propagación
  eventual de npmjs. Su contrato es "exit 0 con el primer stdout no vacío":
  reintenta sólo lo vacío. Los cuatro call sites de `.github/workflows/npm.yml`
  leen (a) `dist.integrity` de una versión exacta, en `publish-platforms`,
  `publish-main` y `verify-and-promote`, y (b) `dist-tags.latest` en
  `verify all latest tags resolve to the release`. Sólo (b) puede devolver un
  valor no vacío y equivocado: tras `npm dist-tag add`, el registro puede
  responder con la versión anterior del tag, que también es no vacía (S1, S2).
- L3: Por eso el valor esperado sólo existe en (b): el `integrity` de una
  versión exacta es impredecible, pero el `latest` promotionado sí es un
  valor conocido de antemano. De ahí que la corrección sea un predicado de
  valor esperado (`--expect`) y no un predicado arbitrario.
- L4: RED observado contra el helper actual (antes de la corrección):
  - `wait-for-registry.sh --expect 0.7.3 -- <npm simulado>` → exit 2,
    `unknown argument: --expect` (la opción no existe).
  - Sin `--expect`, el mismo npm simulado (`0.7.2` y luego `0.7.3`) devuelve
    `0.7.2` con exit 0 tras **una sola lectura**: la segunda respuesta nunca
    se consulta (S2).
  - El stderr del comando se descarta hoy (`2>/dev/null`): un `npm ERR!` real
    nunca llega al log del step (S3).
- L5: Corrección aplicada. `scripts/wait-for-registry.sh` acepta `--expect
  VALUE`: la lectura sólo se acepta cuando devuelve exactamente ese valor; un
  valor obsoleto se reintenta como uno vacío y, al agotar el presupuesto, el
  log nombra el valor esperado y el último observado. El stderr del comando se
  captura a un archivo temporal y se reporta en el log de intentos y en el
  fallo final, en vez de descartarse.
- L6: El cableado usa `--expect` sólo donde el valor esperado se conoce de
  antemano. `dist.integrity` de una versión exacta es impredecible (lo
  genera el registro), así que las tres lecturas de integridad conservan el
  contrato "primer valor no vacío". La lectura de `dist-tags.latest` sí
  conoce el valor esperado (la versión promovida), y es exactamente la que
  la issue describe.
- L7: El check diferencial con el helper anterior detectó un falso positivo
  propio (el estado del `npm` simulado no se borraba entre corridas) y, ya
  corregido, confirma que stdout, stderr y exit code no cambian para los tres
  call sites sin `--expect`.
