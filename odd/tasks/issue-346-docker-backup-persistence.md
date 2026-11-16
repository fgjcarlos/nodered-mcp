# Issue #346 — writable and persistent backups for the nonroot image

## Specs

- S1. "La etapa de runtime utiliza distroless nonroot, sin fijar un `WORKDIR` escribible ni un `NODERED_BACKUP_DIR` adecuado. El default es `backups`, relativo al directorio de trabajo del runtime. Bajo el directorio raíz habitual de la imagen, el usuario nonroot no puede crear `/backups`; los writes fallan al intentar hacer el backup." (issue #346)
- S2. "Los ejemplos de Docker tampoco montan un directorio persistente." (issue #346)
- S3. "Corrección/aceptación: fijar una ruta escribible, documentar/montar persistencia y añadir un smoke que realice una edición real y compruebe el backup y su supervivencia al recrear el contenedor. El smoke actual solo prueba auth e initialize." (issue #346)
- S4. "Añadir una regresión que falle con el comportamiento actual y pase con la corrección." (issue #346, validación de cierre)
- S5. "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados." (issue #346, validación de cierre)
- S6. "Actualizar la documentación afectada cuando cambien garantías o límites." (issue #346, validación de cierre)
- S7. "Pendiente de validación dinámica: este hallazgo procede de inspección; no debe presentarse como una reproducción de extremo a extremo." (issue #346)
- S8. "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto." (issue #346)

## Tasks

- T1 (S1, S3) — Fijar en la imagen una ruta escribible para backups (WORKDIR propio del usuario nonroot + `NODERED_BACKUP_DIR` explícito). Route: inline. Commit: —
- T2 (S3, S4) — Smoke real: edición de flows contra un Node-RED stub, comprobar el backup escrito y su supervivencia al recrear el contenedor. Route: inline. Commit: —
- T3 (S2, S6) — Documentar el montaje persistente en los ejemplos de Docker y sus límites. Route: inline. Commit: —
- T4 (S4, S5, S6) — Comprobaciones, regresión y cierre. Route: inline. Commit: —
- T5 (S3, S4, S6) — Corrección de los hallazgos de la revisión nativa. Route: inline. Commit: —

## Native review

Lineage `review-3a629f3f13170519`, tier high, 4 lentes (risk, resilience,
readability, reliability), candidato `0f369fb8…` (243 líneas, 9 paths).
Los cuatro reviewer corrieron y el refuter чеquearon los hallazgos; el
lineage cerró **escalated** (no aprobado), con `unknown_causality` sobre
`R3-volume-root-ownership`:

- `R4-RFC3339-EXTRACTION` (resilience, CRITICAL, corroborado por el
  refuter) — el smoke extraía el nombre del snapshot con un patrón
  genérico y, si no había coincidencia, `grep -q ""` haría que la
  comprobación de persistencia fuera vacua. **Corregido**: el patrón
  ahora es el del formato real y un nombre vacío corta el smoke. El
  refuter se apoyó en `docs/architecture.md`, que decía `flows-<RFC3339>.json`
  cuando el código escribe `flows-<YYYYMMDD-HHMMSS.NNNNNNNNN>-<seq>.json`
  (UTC). El doc estaba equivocado: **corregido**.
- `R3-volume-root-ownership` (reliability, CRITICAL, causalidad
  desconocida) — el reviewerministrador afirma que un volumen nombrado
  vacío montado sobre el path de la imagen deja una raíz propiedad de
  root y que el usuario nonroot no puede escribir. **No se reproduce**:
  Docker copia el directorio de la imagen —con su propiedad— al volumen
  vacío, y el snapshot se escribió ahí y sobrevivió a la recreación del
  contenedor (Docker 29.8.1, observado). La garantía queda escrita en
  `docs/configuration.md` y `docs/troubleshooting.md` para que sea legible
  sin Docker, y el smoke la comprueba en cada corrida.

## Log

- L1. "Resuelve la issue #346" (fgjcarlos, 2026-11-16).
- L2. Auditoría de referencia: `audit-2026-10-19 · A19 · P1`, sobre `main @ 93f47b1`; el hallazgo es de inspección, no de reproducción end-to-end (S7).
- L3. **Validación dinámica (Docker 29.8.1 local).** El hallazgo tal como está escrito no se reproduce: la imagen vieja (`origin/main`) tiene `WORKDIR=/home/nonroot` heredado de distroless, que sí es escribible por el uid 65532, así que el write del snapshot **funcionaba** y `set_flows` completaba. Lo que sí falla de forma reproducible es la otra mitad: el snapshot caía en la capa escribible del contenedor y **no sobrevivía a la recreación** (el smoke nuevo falla contra la imagen vieja con `No backups saved yet.`). La corrección fija el path de forma explícita en vez de heredarlo, y añade el montaje persistente que S2 pedía.
- L4. Un volumen nombrado montado sobre un path que la imagen **no** tiene hereda `root:root` y el usuario nonroot no puede escribir en él: el path del backup tiene que existir en la imagen con su propiedad, o el snapshot falla con `permission denied` (observado y corregido durante la validación).
