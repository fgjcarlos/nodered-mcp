# Issue #350 — alinear garantías de seguridad, referencias de tools y roadmap con lo ya implementado

Riesgo declarado: item 3 (contratos: la documentación que otros consumen) y item 1
(datos: la documentación de recuperación). Sin cambios de lógica de runtime.

Estado: **implementación autorizada y verificada** (alcance acordado con el
usuario, L2; comprobaciones en L5).

## Specs

- S1. "**P3.** [docs/roadmap.md] mantiene pruning como pendiente aunque ya existe, y presenta v0.7 como futura cuando la última pública es v0.7.2." (issue #350)
- S2. "[docs/tools.md] describe `set_context` con un endpoint POST de contexto que no implementa: en realidad usa el helper y un inject." (issue #350)
- S3. "README/SECURITY generalizan el backup previo y la validación a todas las mutaciones, pero la paleta no hace snapshot de flujos y los full deploys no comparten el validador." (issue #350)
- S4. "Corregir el lenguaje de recuperación de credenciales, las garantías de denylist y los comentarios obsoletos." (issue #350)
- S5. "**Aceptación:** documentación de operaciones, riesgos y recuperación coincide con la implementación; changelog recoge todos los cambios relevantes posteriores a 0.7.2. Actualizar versión del paquete y pins exactos de dependencias opcionales como paso normal de preparación, sin contar que siga en 0.7.2 como bug actual." (issue #350)
- S6. "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto." (issue #350)
- S7. "Contrastar cada afirmación modificada con el comportamiento y los tests existentes. Ejecutar las comprobaciones adecuadas al alcance y registrar resultados. Actualizar la documentación afectada cuando cambien garantías o límites." (issue #350, validación de cierre)

## Tasks

- T1 (S1) — `docs/roadmap.md`: pruning de backups ya implementado, v0.7 entregado, contradicción del caché local resuelta. Route: inline. Commit: `8fb4224`
- T2 (S2, S3) — `docs/tools.md`: clasificación de riesgo sin prometer backup universal, matiz de la denylist, nueva sección "Which writes snapshot the flow configuration", aviso de límites de recuperación acotado a las tools que aplican. Route: inline. Commit: `8fb4224`
- T3 (S3, S4) — `README.md`, `README.es.md`, `SECURITY.md`, `CONTRIBUTING.md`, `docs/architecture.md`, `docs/troubleshooting.md`: alcance real de snapshot, superficie de denylist, lista de deployers, conteos de tools. Route: inline. Commit: `8fb4224`
- T4 (S4) — Comentarios obsoletos: `internal/nodered/backup.go` ("BEFORE any mutating operation"), `internal/mcp/setcontext.go` y `internal/mcp/server.go` (mutex de contexto), doc comments duplicados en `tools_context.go`, `tools_backups.go` y `tools_shared.go`. Route: inline. Commit: `docs: drop the stale comments about the backup and the helper lock (#350)`
- T5 (S5) — `CHANGELOG.md`: entradas faltantes de #330, #331, #332, #333, #334 y #345, y secciones de release 0.7.1 / 0.7.2. Sin bump de versión. Route: inline. Commit: `d0ce679`
- T6 (S7) — Contraste de cada afirmación contra el código, suite completa y cierre de la issue. Route: inline. Commit: `docs: drop the stale comments about the backup and the helper lock (#350)`

## Delivery

- Rama: `fix/issue-350-docs-alignment`, desde `origin/main` (`72a3113`).
- Commits (tres slices revisables):
  - `8fb4224` — `docs: state the real snapshot and denylist guarantees (#350)`
  - `d0ce679` — `docs(changelog): complete the entries since v0.7.2 (#350)`
  - `docs: drop the stale comments about the backup and the helper lock (#350)`
- Ningún cambio de lógica: el diff en `.go` toca solo comentarios.

Revisión nativa (RDD on, `assess` risk=medium, nativeRisk=high por la señal
`SECURITY.md`, `reviewDue=true`): lineage `review-b3d1ef4e937fec6a`, lentes
`readability` y `risk`, cierre **approved** con la autoridad quemada. Dos
hallazgos informativos, sin transición de corrección:

- `R2-READ-001` (readability, WARNING): la deduplicación de los doc comments
  había borrado las dos copias del comentario de `flowParam`, dejando la
  función sin documentar. Corregido: los nueve comentarios de
  `internal/mcp/tools_shared.go` están restaurados (uno por función).
- `R2-READ-002` (readability, WARNING): el comentario de `snapshotFlows`
  nombraba solo la paleta y `inject_node` como excepciones, pero
  `set_flows_state` también snapshot (no cambia los flows, arranca o para el
  runtime). Corregido: el comentario lo dice.

Commits tras la revisión: `docs: restore the doc comments dropped with their
duplicates (#350)`.

## Log

- L1. Petición original del usuario, textual: "resuelve la issue #350".
- L2. Alcance acordado con el usuario antes de escribir: (a) solo
  documentación y comentarios, sin tocar la lógica de runtime —el acceptance
  de la issue dice que la documentación debe coincidir con la implementación,
  no al revés—; (b) completar el CHANGELOG con las entradas faltantes y las
  secciones 0.7.1 / 0.7.2, sin tocar la versión del paquete ni cortar release.
  `package.json` ya está en 0.7.2 con las `optionalDependencies` pineadas
  exactamente, así que ahí no hay bug pendiente (S5 lo dice explícitamente).
- L3. Premisas de la issue que el código desmiente, verificadas antes de
  escribir: `docs/tools.md` **no** documenta un endpoint `POST /context`
  (grep de `POST /context` en `docs/`: 0 coincidencias); la fila de
  `set_context` ya decía `GET /flows`, `POST /flow`, `POST /inject/:id`, que
  es exactamente lo implementado (`internal/mcp/setcontext.go:252-283`,
  `internal/mcp/tools_context.go:169-186`). El lenguaje de recuperación de
  credenciales tampoco promete un refresh o re-auth: el cliente no tiene
  manejo de 401 (`internal/nodered/client.go:293-299`); los documentos ya
  decían que los snapshots son estructurales y no recuperan credenciales.
  Lo que sí era falso: el backup previo generalizado a toda mutación
  (paleta e `inject_node` no snapshot), la lista de denylist sin sus dos
  excepciones, los conteos de tools de `docs/architecture.md` (21 read, no
  20) y el pruning del roadmap.
- L4. Evidencia de la línea de base antes de editar: snapshot en
  `internal/nodered/flows.go:164,231,336`, `flows_update.go:83`,
  `settings.go:80,160`, `subflows.go:868`; sin snapshot en
  `internal/nodered/nodes.go:58,84,102` (paleta) y `flows.go:354,393`
  (`inject_node`). Validadores: `validateFlowWires` (`flows.go:578`) para la
  escritura de un tab y `validateEffectiveConfig`
  (`internal/nodered/validate_deploy.go:158`) para la configuración
  completa; `validateTabEntries` (`validate_deploy.go:177`) es la regla que
  aplica la definición de subflow ausente a la escritura de un tab.
- L5. Comprobaciones: `go build ./...` OK; `gofmt -l internal cmd` limpio;
  `go test ./...` OK (los seis paquetes, 0 fallos); `go vet ./...` OK;
  `git diff --check` limpio. Conteos verificados contra el código:
  44 tools, 21 read, 23 mutantes, 3 recursos, 2 prompts.
