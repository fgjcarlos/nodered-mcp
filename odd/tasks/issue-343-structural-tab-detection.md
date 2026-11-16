# Issue #343 — detect tab entries structurally rather than by JSON byte fragments

## Specs

- S1. "La detección de tab usa el fragmento literal `"type":"tab"`. El JSON válido `{ "id": "t", "type": "tab" }` se rechaza como si no incluyera tabs." (issue #343)
- S2. "Corrección: decodificar el campo `type` de cada entrada. No usar coincidencia textual para validar estructura JSON." (issue #343)
- S3. "Aceptación: documento compacto, con indentación y pasado como objeto producen el mismo resultado; textos dentro de propiedades no se confunden con un tab real." (issue #343)
- S4. "Añadir una regresión que falle con el comportamiento actual y pase con la corrección." (issue #343, validación de cierre)
- S5. "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados." (issue #343)
- S6. "Actualizar la documentación afectada cuando cambien garantías o límites." (issue #343)
- S7. "Seguimiento del alcance descrito arriba; no se afirma que las correcciones anteriores no existan. No reabrir ni rehacer trabajo ya resuelto." (issue #343)

## Tasks

- T1 (S1, S2, S7) — Localizar la regla de tab y su historia. Route: read. Commit: —
- T2 (S3, S4) — Regresión en el cliente: entrada indentada aceptada, tab dentro de una propiedad no cuenta. Route: inline. Commit: —
- T3 (S3, S4) — Regresión en la herramienta: compacto, indentado y array de objetos dan el mismo resultado. Route: inline. Commit: —
- T4 (S4) — Observar RED contra la implementación histórica de fragmentos de bytes. Route: scratch. Commit: —
- T5 (S5, S6) — Comprobaciones y documentación. Route: inline. Commit: —

## Log

- L1: Petición original: "Ahora resuelve la issue #343" (2026-11-16).
- L2: La regla de tab ya no usa fragmentos de bytes: `hasTabEntry`
  (`internal/nodered/settings.go:161`) decodifica el campo `type` de
  cada entrada. La implementación por fragmentos de bytes
  (`bytes.Contains(bytes.ToLower(f), []byte(`"type":"tab"`))`) entró en
  `b5498d2` (#106) y fue sustituida en `b7f11e7` (PR #363, issue
  #334). No hay ninguna búsqueda textual de un tab en el código de
  producción: `grep -rn '(strings|bytes).Contains.*tab' internal/ cmd/`
  no devuelve nada.
- L3: Lo que faltaba era la cobertura: ningún test pasaba un documento
  indentado por la ruta de `flows`, ni pinnaba que un tab mencionado
  dentro de una propiedad no es un tab. El alcance de este trabajo es
  esa regresión y la garantía documentada (S3, S4, S6); no se rehace el
  código ya corregido (S7).
- L4: RED observado contra la implementación histórica restaurada en
  una copia scratch (`mktemp -d`, nunca commiteada):
  `TestSetFlows_AcceptsAnIndentedTabEntry` falla con
  `flows: array must contain at least one element with type "tab"`,
  `TestSetFlows_DoesNotCountATabMentionedInsideAProperty/nested_object`
  despliega un array sin tab (`server should not be called`), y
  `TestHandleSetFlows_TabRuleIsIndependentOfTheJSONForm/with_a_tab/indented_string`
  falla en la herramienta.
