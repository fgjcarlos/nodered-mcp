# Issue #342 — Redact sensitive URL components in getRaw HTTP errors

## Objective
Stop the URL of the configured Node-RED runtime (query string and userinfo included)
from reaching an error message, a log line or the MCP client. An error must name a
path without query nor userinfo, so a token embedded in the base URL cannot leak
through a 4xx, a 5xx, a network failure or an invalid URL.

## Specs
S1: "**Código:** [internal/nodered/client.go:getRaw](https://github.com/fgjcarlos/nodered-mcp/blob/93f47b19a4edda1806404897df266f1a43630b11/internal/nodered/client.go), rama de respuesta no 2xx."

S2: "Los errores de transporte están redactados, pero `APIError.Path` recibe la URL completa (`joined`). Un error 404 en `/flow/global` con URL base que contiene un parámetro de token genera un error con ese parámetro intacto, que puede llegar a logs y al cliente MCP."

S3: "**Corrección:** usar una ruta sin query ni userinfo en todos los errores; revisar también errores de configuración y respuestas mal formadas, sin prometer redacción de secretos arbitrarios devueltos por un servidor externo."

S4: "**Aceptación:** tokens ficticios en URL no aparecen en errores ni logs ante 4xx, 5xx, fallos de red y URL inválida."

S5: Validación de cierre: "Añadir una regresión que falle con el comportamiento actual y pase con la corrección." / "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados." / "Actualizar la documentación afectada cuando cambien garantías o límites."

S6: "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto."

S7: "**Evidencia:** Fixture con parámetro de token sintético."

## Tasks
- T1 (S1, S2, S3): the non-2xx branch of `getRaw` records a path without query nor userinfo in `APIError.Path`, instead of the joined URL.
- T2 (S3, S4): the configuration errors of the request path (base URL parse, URL join, joined URL parse, request build) report a redacted URL and a reason that never echoes the raw URL.
- T3 (S7): regressions that fail on the current behaviour and pass on the fix — 4xx, 5xx, network failure and invalid URL, with a synthetic token in the base URL.
- T4 (S5): the affected documentation and the CHANGELOG record the new guarantee, then the scoped verification run.

## Progress

- T1 done: `getRaw` records `redactedPath(joined)` in `APIError.Path`, so
  the error names the request path (`/flow/global`) instead of the
  joined URL. The JSON path (`doURL`) already recorded the path
  segment, so both surfaces now render the same shape.
- T2 done: the configuration errors of both request paths (base URL
  parse, URL join, joined URL parse, request build) go through the new
  `urlErrorReason`, which drops the URL-bearing `*url.Error` wrapper
  and keeps the reason. The message no longer echoes the URL it
  failed on.
- T3 done: `internal/nodered/apierror_redaction_test.go` — 4xx, 5xx,
  network failure and invalid base URL, with a synthetic token in the
  query string and a synthetic password in the userinfo.
- T4 done: `docs/configuration.md` (new "Tokens in the URL" section) and
  `CHANGELOG.md` record the guarantee and its limit.

## Verification evidence

- RED observed before the fix: the 4xx and the 5xx recorded
  `APIError.Path = "http://user:hunter2@127.0.0.1:40009/flow/global?token=SECRET"`,
  and the invalid base URL rendered `parsing base URL: parse
  "http://127.0.0.1:1x/?token=SECRET": invalid port ":1x" after host`.
  The network-failure case was already redacted.
- GREEN after the fix: `go test ./...`, `go test -race ./...`,
  `go vet ./...`, `gofmt -l .` (empty), `git diff --check` clean,
  `bash scripts/coverage-ratchet.sh` OK (86.20% vs 81.20% baseline),
  `bash scripts/complexity-check.sh` OK.
- Differential check against the base commit `bdcb36e` (the same
  program, run in both trees): `json-404`, `json-ok`, `raw-deadport`
  and `json-deadport` are byte-identical, so no surface the request
  did not name changed. The lines that changed are `raw-404`,
  `raw-500` and `raw-network` (the joined URL became the request path)
  and `json-badurl` / `raw-badurl` (the raw URL in the configuration
  error became the reason alone).

## Log
- L1 (2026-11-16, user): "Resuelve la issue #342, antes cierra la issue #341" — issue #342 "[P2][A15] redact sensitive URL components in getRaw HTTP errors" (audit-2026-10-19 · A15 · P2), labels bug/security/qa. Base commit `bdcb36e`.
- L2: issue #341 closed first: its fix already landed in `main` through PR #377 (`bdcb36e`), verified green with `go test ./internal/mcp/ ./internal/nodered/`.
- L3: Design decisions taken from the issue text: `APIError.Path` becomes the request path (no query, no userinfo), matching what `doURL` already records for the JSON endpoints; the body of an error stays untouched, because the issue explicitly does not promise redaction of arbitrary secrets returned by an external server.
