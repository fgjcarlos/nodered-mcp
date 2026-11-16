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

Lineage `review-bd0ec40c64b8b910`, tier high (two lenses: resilience,
reliability), correction budget 108. The lineage was created and the
authority is intact, but the reviewer capture never ran: every
`gentle_review_capture` / `gentle_review_capture_group` call is rejected
by the facade with `capture-route-registration-rejected` /
`selector-mismatch` ("Provider collectBinding collides with a different
registered route"), and the same error appears when the STATUS is read
with the provider base tree or with the base commit instead of
`origin/main`.

The blocker is `authority-unavailable` / `provider-unavailable` in the
harness's capture-route registration, not a finding about this change:
the collect binding read from STATUS is byte-identical to the one
submitted (canonical form, idempotent under re-canonicalisation), and
the retained route's selector is the only field in conflict. The
facade's own guidance for this state is to preserve the diagnostic and
use the supported native continuation — never to reset or rebuild the
authority locally — so no reviewer verdict exists for this candidate
and none is claimed.

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
