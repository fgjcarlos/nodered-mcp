# Issue #349 — Portón de publicación sobre el SHA exacto del tag

Rama: `fix/issue-349-release-sha-gate`, desde `origin/main` (`e2d1a45`).
Riesgo declarado: item 5 (entrega/entorno: CI y publicación) e item 3 (contratos: canales de publicación y etiqueta `latest`).

Estado: **plan aprobado, implementación autorizada** (decisiones del usuario, L8).

## Specs

S1. "audit-2026-10-19 · A22 · P2".

S2. "Código: [.github/workflows/release.yml](https://github.com/fgjcarlos/nodered-mcp/blob/93f47b19a4edda1806404897df266f1a43630b11/.github/workflows/release.yml), `docker.yml`, `ci.yml`."

S3. "El workflow de release valida archivos y ejecutables, pero no depende del resultado de las pruebas funcionales/race/vet del mismo SHA. El workflow Docker publica en paralelo por tag. Puede lanzarse un tag cuyo commit no haya pasado CI y publicar alguno de los canales. No se ha hecho una publicación para demostrarlo; la conclusión deriva del grafo de jobs y triggers."

S4. "Corrección/aceptación: una puerta común de release sobre el SHA exacto, o reutilización de CI antes de publicar los canales. Simular una prueba funcional fallida y comprobar que no se publica ningún canal. Evitar que una prerelease o un rerun antiguo cambie `latest` de una release estable más reciente."

S5. "Añadir una regresión que falle con el comportamiento actual y pase con la corrección."

S6. "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados."

S7. "Actualizar la documentación afectada cuando cambien garantías o límites."

S8. Contexto verificado en el código: `ci.yml:3-8` dispara en `push` a `branches: [main]`, `pull_request` y `workflow_dispatch`. Un push de tag **no** produce ninguna ejecución de CI para su SHA, así que hoy no existe un resultado de CI que consumir para el commit tagueado.

S9. Contexto verificado en el código: `release.yml:3-6` dispara en `push` a `tags: "v*"` y publica el GitHub Release con una cadena propia (`release-snapshot` → `release-artifact-smoke` → `goreleaser`, `release.yml:20/53/76`) que no incluye ninguna prueba funcional, `-race` ni `go vet`. `docker.yml:3-10` dispara en el mismo `push` de tag y su único job `build` (`docker.yml:17-18`) no tiene `needs:` ni `if:`: hace `push: true` a GHCR sin ninguna puerta.

S10. Contexto verificado en el código: `npm.yml:36-39` es el único canal con puerta, vía `workflow_run` de `release`, condicionada a `github.event.workflow_run.conclusion == 'success'` y `startsWith(github.event.workflow_run.head_branch, 'v')` (`npm.yml:56-58`, `npm.yml:224-226`). Es una puerta **transitiva sobre `release.yml`** (snapshot + smoke), nunca sobre `ci.yml`, y se keyea por **nombre de tag**, no por SHA: `head_sha` no se referencia en ningún workflow del repositorio.

S11. Contexto verificado en el código: ningún workflow declara `workflow_call` ni `concurrency` (grep sin coincidencias). `.goreleaser.yaml` no tiene bloque `release:`, así que rigen los defaults de GoReleaser v2 (`prerelease: auto`, `make_latest: true`), y `docker/metadata-action` rige con `flavor.latest=auto` por defecto, más la entrada `type=raw,value=latest,enable={{is_default_branch}}` (`docker.yml:51`), que en un push de tag siempre es falsa y por tanto **no** protege el canal: el `latest` automático se aplica igual.

S12. Contexto verificado en el código: existe precedente de regresión sobre workflows. `bin/npm_workflow_order_test.js:24` lee `.github/workflows/npm.yml` y afirma sobre su texto la existencia y el orden de los jobs (`needs:`, `if:`, pasos de promoción), y `ci.yml:130-137` ejecuta `node bin/*_test.js` en el job `npm-wrapper`. Ese es el mecanismo de regresión disponible en este repositorio para el comportamiento de los workflows.

S13. Contexto verificado en el código: la suite de CI cubre `gofmt` (`ci.yml:28`), `go vet ./...` (`ci.yml:55`), `go test ./...` (`ci.yml:105`), `go test -race ./...` (`ci.yml:158`), `govulncheck` (`ci.yml:66`), el ratchet de cobertura (`ci.yml:77`), el gate de complejidad (`ci.yml:87`), `docker-smoke` (`ci.yml:145`), el snapshot de release (`ci.yml:188`) y el smoke de artefactos (`ci.yml:224`).

## Tasks

- T1 | S4, S5, S12 | inline (parent) | RED: regresión Node que modela el grafo de jobs de los cuatro workflows y afirma que, con la puerta fallida, **ningún** job de publicación (goreleaser, docker push, npm publish, npm dist-tag) es alcanzable; y que `ci.yml` es invocable como workflow reutilizable. Falla hoy: `release.yml` y `docker.yml` no tienen puerta. | commit: `6a1039a`
- T2 | S4, S8 | inline (parent) | `ci.yml`: añadir el trigger `workflow_call` conservando `push`/`pull_request`/`workflow_dispatch`. Al ser invocado desde un workflow del mismo repositorio, el `github` context apunta al caller, así que `github.sha` es el commit del tag y el checkout por defecto ya trabaja sobre el SHA exacto. | commit: `6a1039a`
- T3 | S4, S9 | inline (parent) | `release.yml`: añadir el job `gate` (`uses: ./.github/workflows/ci.yml`) y encadenar `release-snapshot: needs: gate`. Toda la cadena de publicación queda colgando de la puerta por el mecanismo `needs` (un fallo de la puerta corta la cadena). | commit: `6a1039a`
- T4 | S4, S9 | inline (parent) | `docker.yml`: mismo job `gate` y `build: needs: gate`. Es el canal hoy sin ninguna protección. | commit: `6a1039a`
- T5 | S4, S10 | inline (parent) | `npm.yml`: anclar al SHA exacto — usar `github.event.workflow_run.head_sha` como `ref` del checkout y afirmar que el commit del tag y el `head_sha` del run gateado coinciden, conservando `conclusion == 'success'`. | commit: `6a1039a`
- T6 | S4, S11 | inline (parent) | Política del canal `latest`: script `scripts/release-latest-guard.js` con una sola regla — *una publicación mueve `latest` solo si su tag es una versión estable y es la versión estable más nueva del repositorio* — y su uso en los tres canales: `make_latest: false` en `.goreleaser.yaml` más un `gh release edit --latest` condicional en `release.yml`, `flavor: latest=false` más entrada `latest` condicional en `docker.yml`, y guarda de promoción en `npm.yml` (que hoy promueve `latest` sin distinguir prerelease: `npm.yml:402`, `npm.yml:415`). | commit: `ffc63c4`, `6a1039a`
- T7 | S4 | inline (parent) | `concurrency` en los workflows de tag y en `npm.yml`, para que dos runs del mismo tag o dos tags simultáneos no compitan por el canal `latest`. | commit: `6a1039a`
- T8 | S5, S6, S7 | inline (parent) | Comprobaciones del alcance: suite de Node del job `npm-wrapper`, `go build ./...`, `go vet ./...`, `gofmt -l .`, `git diff --check`, suite completa `go test ./...`, ratchet de cobertura y gate de complejidad. Documentación afectada (`CONTRIBUTING.md`, proceso de release) y `CHANGELOG.md`. | commit: `fd1eada`, (docs del PR)

## Diseño

**Puerta común (S4, primera opción de aceptación).** Un único job `gate` que invoca `ci.yml` como workflow reutilizable, encadenado con `needs:` en cada workflow que publica. Ventajas frente a comparar SHAs entre workflows: el SHA publicado es el del tag **por construcción** (mismo run, mismo `github.sha`), no por comparación; no depende del comportamiento de `workflow_run` (que ejecuta el workflow de la rama por defecto y fires en cada re-run); y el resultado es visible en la UI del run. Coste conocido: la suite se ejecuta dos veces en el camino de release (una como puerta, otra como `release-snapshot` dentro de CI) — minutos de runner extra a cambio de una garantía estructural.

**Canal `latest` (S4, segunda mitad de la aceptación).** Hoy los tres canales mueven `latest` sin comparar: goreleaser con `make_latest: true` por default, docker con el `latest=auto` de `metadata-action` (que excluye prereleases pero **no** impide que un tag estable antiguo sobrescriba `latest`), y npm con `npm dist-tag add ... latest` incondicional. La regla propuesta es única y verificable: *mueve `latest` solo el tag estable más nuevo*. Se implementa con un script Node testeable en isolation (`scripts/release-latest-guard.js`), alimentado por la lista de tags del checkout (`git tag`, con `fetch-depth: 0`), que devuelve "promover" o "no promover" y por qué.

**Regresión (S5).** `bin/release_gate_test.js` (nuevo, junto a `bin/npm_workflow_order_test.js`), ejecutado por el job `npm-wrapper` de `ci.yml`. Cubre: (a) el grafo de jobs de los cuatro workflows — cada job que publica tiene la puerta en su cierre `needs` transitivo; (b) la simulación de una prueba funcional fallida — con la puerta en `failure`, ningún job de publicación es alcanzable; (c) `ci.yml` declara `workflow_call`; (d) la regla de `latest`: prerelease → no promueve, tag estable antiguo → no promueve, tag estable más nuevo → promueve.

**Límite de la evidencia (honestidad).** La aceptación pide "simular una prueba funcional fallida y comprobar que no se publica ningún canal". En este repositorio la regresión automatizada posible es **estructural** (T1), como ya se hace con `npm.yml`. La verificación de extremo a extremo (etiquetar un commit con una prueba rota y comprobar que no se publica nada) requiere un tag real y publicaría de verdad: se documenta como procedimiento manual opcional, no como regresión automatizada.

## Log

L1. Petición del usuario (2026-11-17), verbatim: "Realiza un plan para resolver la issue #349, si tienes dudas realiza un cuestionario".

L2. Alcance leído: el hallazgo menciona `release.yml`, `docker.yml` y `ci.yml`, pero el repositorio tiene un cuarto canal de publicación, `npm.yml`, que la issue no nombra. La aceptación dice "no se publica ningún canal", así que el plan incluye los cuatro workflows.

L3. Evidencia recogida por inspección (la propia issue declara que no es una reproducción de extremo a extremo): `ci.yml` no corre para tags (S8); `docker.yml` publica sin puerta (S9); `npm.yml` está gateado solo de forma transitiva sobre `release.yml` y por nombre de tag, no por SHA (S10); no hay `workflow_call` ni `concurrency` en ningún workflow (S11).

L4. Precedente de regresión: las regresiones sobre workflows en este repositorio son scripts Node que parsean el YAML como texto (`bin/npm_workflow_order_test.js`), ejecutados por el job `npm-wrapper` (`ci.yml:130-137`). El plan sigue ese precedente en vez de introducir un framework de tests de workflows.

L5. Defaults externos verificados en la documentación oficial: GoReleaser v2 usa `release.prerelease: auto` y `release.make_latest: true` por default, y `make_latest` admite plantillas desde v2.6 (el repo fija v2.18.0); `docker/metadata-action` usa `flavor.latest=auto` por default, que excluye prereleases pero no protege frente a un tag estable anterior.

L6. Decisiones abiertas, planteadas al usuario en el cuestionario: (a) mecanismo de la puerta — workflow reutilizable `workflow_call` (recomendado), `workflow_run` con comparación de SHA, o reutilizable más CI también en tags; (b) alcance de la política de `latest` — regla completa, solo prereleases, o solo el gate; (c) qué hacer cuando el tag no es el estable más nuevo — publicar los artefactos versionados y omitir `latest`, abortar todo, o publicar y marcar el run como fallido; (d) alcance de la entrega — solo el plan, plan más implementación, o implementación por bloques con checkpoint.

L8. Respuestas del usuario (2026-11-17): (a) **reutilizable + CI también en tags** — `ci.yml` gana `workflow_call` y además dispara en `push` a `tags: "v*"`, para que el commit tagueado tenga una ejecución de CI visible; (b) **regla completa** de `latest` en los tres canales; (c) **publicar los artefactos versionados y omitir solo `latest`**, con el run en verde y una nota que explique por qué; (d) **plan + implementación completa**, con commits por unidad de trabajo y PR contra `main`.

L9. Consecuencia de L8(a) que hay que registrar: la suite de CI se ejecutará tres veces por tag (una standalone por el trigger `tags`, y la puerta reusable dentro de `release.yml` y dentro de `docker.yml`). Es el precio de que el commit tagueado tenga a la vez una ejecución visible y una puerta estructural; queda como costo conocido a revisar por el mantenedor.

L10. Decisiones de implementación tomadas sobre esa base: la puerta es un job `gate` con `uses: ./.github/workflows/ci.yml`, y cada job que publica cuelga de ella por `needs`; el SHA publicado es el del tag por construcción (mismo run, mismo `github.sha`), sin comparar SHAs entre workflows. La regla de `latest` se implementa con un script Node (`scripts/release-latest-guard.js`) que decide "promover / no promover" a partir del tag y de la lista de tags del repositorio, y se aplica en los tres canales: `make_latest: false` en `.goreleaser.yaml` más un `gh release edit --latest` condicional, `flavor: latest=false` más una entrada `latest` condicional en `docker.yml`, y una guarda de promoción en `npm.yml`. `npm.yml` deja de depender del nombre del tag para el código publicado: el checkout pasa a `github.event.workflow_run.head_sha` y se verifica que el tag resuelva a ese mismo commit.

L11. Implementación de la puerta (T2, T3, T4, T5). `ci.yml` gana el trigger `workflow_call` y el trigger `tags: "v*"`; `release.yml` y `docker.yml` abren con un job `gate` (`uses: ./.github/workflows/ci.yml`) del que cuelga toda la cadena de publicación por `needs`. Como un workflow reutilizable se ejecuta en el contexto del caller, `github.sha` es el commit tagueado y la suite que hace de puerta es la de ese SHA exacto: no hay comparación de SHAs entre workflows que pueda desalinearse. `npm.yml` deja de usar el nombre del tag para el código que publica: el checkout pasa a `github.event.workflow_run.head_sha` y un paso nuevo comprueba que el tag resuelve a ese mismo commit.

L12. Implementación de la regla de `latest` (T6). El script decide "promover / no promover" y su razón; los tres canales lo consultan. En `release.yml`, goreleaser publica con `make_latest: false` (`.goreleaser.yaml`) y un paso posterior marca la release como latest solo si la guarda lo permite, con `gh release edit --latest`. En `docker.yml`, `flavor: latest=false` desactiva el `latest` automático de `metadata-action` (que excluía prereleases pero no impedía que un tag estable antiguo sobrescribiera `latest`) y una entrada `type=raw,value=latest` queda habilitada por la guarda. En `npm.yml`, los pasos de promoción y su verificación quedan condicionados a la guarda, y hay un paso que informa por qué se saltó la promoción.

L13. Desviación del plan, registrada: T6 preveía "`make_latest` templado" en `.goreleaser.yaml`; la implementación usa `make_latest: false` estático más `gh release edit --latest` condicional. Motivo: la plantilla de goreleaser exigiría variables de entorno presentes en todos los builds (incluidos los snapshots de CI), mientras que la versión estática no depende del contexto y deja la decisión en un único lugar, el paso de la guarda.

L14. Conflicto detectado y corregido durante la implementación: al entrar la puerta, `ci.yml` y `release.yml` corren en el mismo run y los dos subían un artefacto llamado `release-snapshot-dist`; dos artefactos no pueden compartir nombre en un run, así que el segundo upload habría fallado y roto la release. El artefacto de `release.yml` pasó a llamarse `release-publish-dist` y la regresión `bin/release_gate_test.js` fija que los dos nombres no coinciden.

L15. Comprobaciones del alcance, todas en verde sobre `6a1039a`: suite de Node del job `npm-wrapper` (los nueve `bin/*_test.js`, incluidos `release_gate_test.js` con 16 aserciones y `release_latest_guard_test.js` con 15), `go build ./...`, `go vet ./...`, `gofmt -l .` (limpio), `git diff --check` (limpio), suite completa `go test ./... -count=1` (ok en los 6 paquetes), `scripts/coverage-ratchet.sh` (86,20% contra un piso de 81,20%) y `scripts/complexity-check.sh` (OK). Los cinco archivos YAML modificados se validaron con el parser de PyYAML.

L16. Entrega: rama `fix/issue-349-release-sha-gate` desde `origin/main` (`5eb42f3`), tres commits (`ffc63c4` script y pruebas de la regla de `latest`, `6a1039a` workflows y regresión estructural, `fd1eada` documentación) y PR #384 contra `main` con `Closes #349` y la etiqueta `type:chore`. La unidad de trabajo son 968 líneas cambiadas, por encima del corte orientativo de ~400: el diff es un solo comportamiento (la puerta de publicación más la regla del canal `latest`) con sus regresiones y su documentación.

L17. Revisión nativa: `gentle_review assess` la clasificó como riesgo medio con riesgo nativo alto y `reviewDue: true`, así que se inició la revisión ordinaria con las cuatro lentes (risk, resilience, reliability, readability) sobre el rango `origin/main..HEAD`. El START creó el linaje `review-cda78ad8b7033511` (tier alto, presupuesto de corrección 200). La recolección no pudo completarse: el paso de collect exige reproducir los bindings opacos del proveedor byte a byte y el host los reemitía alterados, por lo que el grupo fue rechazado seis veces sin mutación alguna. **No hay veredicto de revisión**: ningún revisor llegó a ejecutarse, así que esta revisión no aprueba ni desaprueba nada y no sustituye a las comprobaciones locales. Queda pendiente que el mantenedor, o una sesión cuyo relevo de host pueda reemitir los bindings, retome el linaje.

L18. Incidente del arnés, resuelto en el camino: la primera recolección falló con `package-local-binary-missing` porque el paquete `gentle-pi` se había actualizado en sitio (4.9.0, pin 4.6.0) mientras el proceso de Pi seguía con el módulo viejo en memoria (pin 4.4.0) y su ruta ya no contenía el binario. Se restauró el binario 4.4.0 verificado por digest (sha256 `9f39a94c…`, el mismo que declara el manifiesto `integrity.json` de 4.4.0) en la ruta que el proceso esperaba. No se tocó ningún archivo del repositorio.

L19. Coste conocido que el mantenedor debe decidir: con la decisión L8(a), la suite de CI se ejecuta tres veces por tag (una standalone por el trigger `tags`, y la puerta reusable dentro de `release.yml` y dentro de `docker.yml`). Es el precio de que el commit tagueado tenga a la vez una ejecución visible y una puerta estructural; si el coste de runner importa, la puerta de `docker.yml` puede apoyarse en la ejecución standalone en vez de re-ejecutar la suite.
