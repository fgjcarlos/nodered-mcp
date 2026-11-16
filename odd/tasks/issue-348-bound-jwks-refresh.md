# Issue #348 — Acotar y combinar los refrescos de JWKS para kids desconocidos

Rama: `fix/issue-348-bound-jwks-refresh`, desde `origin/main` (`fb95deb`).
Riesgo declarado: item 2 (seguridad — guarda de caché compartida) e item 4 (concurrencia).

## Specs

S1. "audit-2026-10-19 · A21 · P2".

S2. "Código: [internal/oauth/jwks.go:KeyByID/refresh](https://github.com/fgjcarlos/nodered-mcp/blob/93f47b19a4edda1806404897df266f1a43630b11/internal/oauth/jwks.go)."

S3. "Un kid desconocido fuerza otra descarga incluso con caché fresca. No hay coalescencia de refrescos concurrentes ni espera mínima entre misses. El comentario atribuye los misses a rotaciones infrecuentes, pero el kid llega en tokens aún no verificados y lo controla el solicitante. El rate limit por IP reduce presión, pero no sustituye la protección de la caché compartida."

S4. "Corrección/aceptación: combinar solicitudes simultáneas y limitar refrescos por miss, conservando la posibilidad de rotación. Una batería de tokens inválidos no debe producir una descarga de JWKS por petición."

S5. Cierre: "Añadir una regresión que falle con el comportamiento actual y pase con la corrección."

S6. Cierre: "Ejecutar las comprobaciones adecuadas al alcance y registrar resultados."

S7. Cierre: "Actualizar la documentación afectada cuando cambien garantías o límites."

S8. Contexto verificado en el código: `Keyset` (`internal/oauth/jwks.go`) es la caché TTL-bounded que envuelve a `FetchJWKS`; `KeyByID` es su única entrada pública y el `Verifier` la llama desde la `keyFn` del parser JWT (`internal/oauth/verifier.go`), es decir, una vez por token verificado. El único constructor en producción es `oauth.NewKeyset(md.JWKSURI, 15*time.Minute)` (`cmd/nodered-mcp/main.go`).

S9. Contexto verificado en el código: la ruta TTL (caché vacía o vencida) ya está acotada por el TTL; el refuerzo NO acotado es el refresco por miss, que hoy es un `refresh()` incondicional en cada `KeyByID` con kid desconocido, aunque la caché sea fresca.

## Tasks

- T1 | S3, S4, S5 | inline (parent) | Regresión RED: batería de kids desconocidos y misses concurrentes contra un servidor JWKS que cuenta descargas. | commit: —
- T2 | S4, S8, S9 | inline (parent) | Implementación: coalescencia de refrescos concurrentes y límite temporal entre refrescos por miss, preservando la detección de rotación. | commit: —
- T3 | S4, S7 | inline (parent) | Documentación afectada (`docs/transports.md`) y `CHANGELOG.md`. | commit: —
- T4 | S5, S6 | inline (parent) | Comprobaciones del alcance: suite completa, `go vet`, `-race` del paquete oauth, ratchet de cobertura y gate de complejidad. | commit: —
- T5 | S4 | delegado (`gentle-ai-verify`) | Verificación independiente del cambio de concurrencia y de la garantía de cota (riesgo item 2). | commit: —
- T6 | S1-S7 | inline (parent) | Entrega: commits de unidad de trabajo, push de la rama y pull request contra `main`. | commit: —

## Log

L1. Petición del usuario (2026-11-16), verbatim: "Resuelve la issue #348".

L2. Issue #347 ya está integrada en `main` (`fb95deb`), así que la rama de este trabajo sale de `origin/main` y no arrastra el trabajo de #347.

L3. Evidencia del hallazgo, por inspección y no por reproducción de extremo a extremo (así lo declara la propia issue): en `KeyByID`, tras la comprobación de TTL, el kid se busca en la caché; si no aparece, se ejecuta `k.refresh()` incondicional y se vuelve a buscar. El `kid` proviene del header del token, todavía sin verificar, y lo controla el solicitante: cada token inválido produce una descarga completa del JWKS. Además, `refresh()` no serializa ni comparte el resultado entre llamadas concurrentes, así que N peticiones simultáneas con kids desconocidos producen N descargas.

L4. RED medido sobre `origin/main` con las tres regresiones nuevas (`internal/oauth/jwks_refresh_test.go`, servidor JWKS que cuenta descargas): 50 tokens inválidos con kids distintos → **51 descargas**; 20 misses concurrentes → **40 descargas**; y el tercer caso (un kid publicado dentro de la ventana de miss) se encontraba igual, porque hoy no existe ventana alguna. La regresión es del comportamiento observable (descargas al IdP), no del detalle interno.

L5. Implementación (`internal/oauth/jwks.go`): un contador `gen` bajo el `RWMutex` existente marca la generación de la caché; `refreshMu` serializa los intentos de refresco, de modo que el que llega segundo comparte el resultado en vez de descargar otra vez; `refreshOnMiss` aplica la ventana de miss (`missWindow`, un minuto por defecto) y la consume incluso si la descarga falla, para que un IdP caído no se convierta en un amplificador. La ruta TTL (caché vacía o vencida) conserva su semántica: refresca siempre, ahora coalescido. Se extrajo `findKey` para no duplicar el bucle de búsqueda.

L6. Verificación independiente (`gentle-ai-verify`, riesgo item 2): S3, S4, S5 y S7 cumplidas, sin carreras ni bloqueos (`-race` limpio en 5 corridas, 100 iteraciones de las regresiones nuevas sin flakes), y la ventana de miss efectivamente compartir la descarga entre los que fallan al mismo tiempo. Hallazgos: **F1 (BLOCKER, causado por el cambio)** — los refrescos fallidos se serializaban sin compartir el fallo: contra un IdP que devuelve 503, los 6 llamadores encolados descargaban uno detrás de otro (2,41 s en total, 6 descargas), y en producción (timeout de 10 s) el k-ésimo esperaría k×10 s. El riesgo era real y lo introduje yo al serializar. **WARNING** — un miss puede compartir una descarga iniciada antes de la rotación, con lo que la rotación se acepta hasta una ventana más tarde. **SUGGESTION** — el doc de `NewKeyset` debe decir que las llamadas simultáneas comparten una descarga, y faltaba una regresión que fijara que la ventana se consume aunque la descarga falle.

L7. Corrección de F1: el contador `gen` pasa a ser un contador de *intentos* (`attempt`), que avanza tanto si la descarga sale bien como si falla, y se guarda el error del último fallo (`lastErr`). El que espera en la cola comparte ese resultado —incluido el fallo— en vez de descargar otra vez: contra un IdP caído hay un intento y todos los encolados reciben su error. La caché sigue intacta tras un fallo y la ruta TTL sigue reintentando por petición (disponibilidad), así que una petición que llega después del fallo vuelve a intentarlo sola. Se añadieron las dos regresiones que faltaban (`TestKeyByIDSharesFailedRefreshWithWaiters` y `TestKeyByIDMissWindowHoldsWhileRefreshFails`) y se documentó que una rotación puede aceptarse hasta una ventana más tarde.

L8. Re-verificación independiente (`gentle-ai-verify`, segunda ronda sobre `9746a36`): **F1 corregido y sin bloqueantes**. Medido por el verificador: contra un IdP que devuelve 503 tras 400 ms, 6 llamadores → 1 descarga y 402 ms (antes 6 descargas y 2,41 s); 20 llamadores → 1 descarga y 402 ms (antes 20 descargas y 8,02 s); 40 llamadores que llegan cada 50 ms → 5 descargas y el más lento 402 ms (antes 40 descargas y 14,08 s); con el timeout real de 10 s, un solo intento y nadie espera k×timeout. Disponibilidad verificada: una petición que llega tras el fallo reintenta sola, la caché sobrevive al fallo, los textos de error no cambian y la semántica de `ttl=0` se mantiene. Carrera detectada en 100 goroutines × 20 llamadas contra un IdP que alterna 503 y OK: sin clave errónea, sin bloqueo, sin fuga (83 y 92 descargas contra 2344 y 3053 en la base). Hallazgos restantes: **W1 (WARNING, razonado, no reproducido)** — un llamador encolado en la ruta TTL recibía el error de un intento fallido posterior aunque otro intento hubiera dejado la caché utilizable; se corrige con `cacheUsable()`: si las claves en caché sirven, se usan. **D1 (SUGGESTION)** — faltaba documentar que cada petición posterior a un fallo reintenta sola y que la ventana es por proceso; ya está en `docs/transports.md`. **D2** — la descarga compartida anterior a la rotación queda dentro del límite de una ventana, ya documentado.

L9. Tercera corrección (W1 + D1 + la regresión durable que faltaba): `cacheUsable()` devuelve las claves en caché cuando existen y el TTL no venció, tanto en la ruta TTL como en la de miss, de modo que un fallo ajeno posterior no invalida una caché que otro intento acaba de rellenar; se añadió `TestKeyByIDOutageDoesNotQueueArrivingCallers` (20 llamadores que llegan escalonados durante la caída: el más lento espera menos de 3× el retardo del servidor) y se documentaron los dos residuales de D1. Comprobación diferencial: esa regresión falla en `9450e10` (20 descargas, el más lento 3,5 s) y en `fb95deb` (20 descargas), y pasa en la rama.
