# nodered-mcp — Plan & Playbook

> Un servidor **MCP (Model Context Protocol)** que expone la admin API de Node-RED
> como tools/resources/prompts para clientes IA (Claude Desktop, Claude Code,
> Cursor, VS Code, Gemini CLI, OpenCode, Pi, Cline).
>
> Stack: **Go 1.25+**, single binary, transporte **stdio** o **streamable HTTP**
> (con bearer token o OAuth 2.1).

---

## 0. Estado actual

Última revisión: 2026-07-27. Esta sección es una **instantánea** del árbol
en `main`. El resto del documento describe *intención*; cuando ambas
discrepen, manda esta.

| Capa | Estado |
|---|---|
| `internal/nodered` (cliente HTTP) | Completo: flows, edición granular, palette, settings, contexto, diagnóstico, backups, diff, tail |
| `internal/mcp` (server) | 29 tools, 3 resources, 2 prompts |
| `internal/config` | 13 env vars + flags + validación |
| `internal/oauth` | Resource server OAuth 2.1 / OIDC con JWKS discovery |
| Modelo de datos | Resuelto: JSON opaco, sin pérdida de campos (ver §2) |
| Backup antes de escribir | Implementado y fail-closed (ver §3) |
| Transportes | stdio + streamable HTTP; bearer u OAuth |
| Modo de solo lectura | `--read-only` / `MCP_READ_ONLY`: 14 tools, ninguna de escritura |
| Stream de debug | WebSocket `/comms` con buffer acotado y reconexión |
| CI | `.github/workflows/ci.yml`: formato, vet, tests en 3 SO, race, build cruzado |
| Releases | Tag-triggered en `.github/workflows/release.yml`; npm wrapper en `@fgjcarlos/nodered-mcp`; imagen GHCR |

29 tools, 3 resources, 2 prompts. La división `read` / `write` se aplica en el
*registro* de tools, no dentro de cada handler: las 15 que modifican no llegan
a anunciarse en `--read-only`, de modo que un modelo no puede invocar lo que
no ve. `inject_node` cuenta como modificadora — disparar un inject puede
mandar una orden a un dispositivo real.

---

## 1. Por qué

Node-RED es programación visual de flows. Un MCP habilita un loop nuevo:

> *"Claude, listame los flows → agregá un nodo MQTT que escuche `home/temp` → deployá →
> mostrame los mensajes debug de los últimos 30s"*

…sin abrir el editor ni copiar JSON a mano.

---

## 2. Decisión de diseño clave: los flows son JSON opaco

`RawFlow` está tipado como `json.RawMessage`:

```go
// internal/nodered/types.go:19
type RawFlow = json.RawMessage
```

Node-RED es deliberadamente *schemaless* — cada tipo de nodo tiene su propia
forma. Modelar nodos con structs de Go fijos descartaría en silencio cada
campo no reconocido en un ciclo de lectura y escritura. `nodered-mcp` reenvía
el JSON de flows tal cual y solo interpreta los campos que necesita, en el
punto donde los necesita. No se pierde ningún campo.

**Reglas:**

- Flows y nodos se manejan como **JSON opaco**. Cero pérdida de campos.
- Solo se parsean los campos concretos que se necesitan en el punto donde se
  necesitan (p. ej. extraer `id` / `type` / `label` para un resumen legible).
- El server **nunca reescribe** un nodo que no entiende — lo pasa tal cual.
- Ojo con lo que un LLM edita a ciegas: `wires` (`[[out1, out2], …]`), IDs,
  config nodes compartidos y credenciales (canal aparte).
- **Hallazgo que costó tiempo:** `GET /flow/:id` **no** devuelve un único
  array. Reparte el contenido del tab en `nodes` y `configs`, y la regla la
  decide `runtime/lib/flows/util.js`: un objeto con coordenadas `x` e `y` va a
  `nodes`, cualquier otro va a `configs`. Un broker MQTT compartido vive en
  `configs` aunque pertenezca al tab. Las tools de edición granular respetan
  ese reparto; ignorarlo hacía desaparecer nodos del canvas.
- **Trampa de lectura para nuevos contribuidores:** `grep Extra internal/nodered/`
  devuelve hits en `internal/nodered/edit.go:85`. **No** es el bug de v0.1.
  El bug original era un struct field con tag `json:"-"`, que `encoding/json`
  descarta por completo. El `Extra map[string]json.RawMessage` actual existe
  precisamente para preservar campos desconocidos del tab a través del
  round-trip. La presencia del campo es la cura, no la enfermedad.
- **Estrategia de edición:** ambas, con las granulares por defecto
  (`add_node`, `update_node`, `delete_node`, `connect_nodes`) y `update_flow`
  como salida de emergencia para reescrituras completas. `update_node`
  **fusiona** propiedades en lugar de reemplazar; `delete_node` limpia los
  wires entrantes; `connect_nodes` añade al puerto indicado y hace crecer el
  array si ese puerto no existía.

---

## 3. Seguridad: backup antes de cada escritura (guardrail de v0.1)

Cada operación mutante (`POST /flows`, `PUT /flow/:id`, `DELETE /flow/:id`)
hace, **antes** de tocar nada, `GET /flows` (el flows.json completo) y lo
guarda en disco con timestamp.

- Dir: env `NODERED_BACKUP_DIR` (default `./backups` o temp del OS).
- Nombre: `flows-<RFC3339>.json`.
- Un snapshot del config completo cubre cualquier escritura ("al menos
  flows.json").
- Habilita rollback vía tools `list_backups` / `restore_backup`.
- Node-RED tiene su propio `.backup` de un nivel, pero es local al host y el
  MCP habla por HTTP → no sirve.
- Nombres de backup restringidos a bare names: `restore_backup` no se puede
  usar para leer ficheros arbitrarios del disco.

<!-- ponytail: sin poda de backups; agregar prune (keep last N) cuando el dir crezca -->

---

## 4. Arquitectura

```
MCP Client (Claude, Cursor, OpenCode, Pi, …)
    │
    ├──stdio | streamable HTTP──▶ nodered-mcp (Go)
    │                                │
    │                                ├──HTTP──▶ Node-RED admin API :1880
    │                                │
    │                                ├──WebSocket──▶ Node-RED /comms (debug tail)
    │                                │
    │                                └──HTTPS──▶ idp.example/.well-known/openid-configuration
    │                                              (OAuth 2.1 resource-server mode)
    │
    └──Bearer | JWT──▶ nodered-mcp (transporte HTTP autenticado)
```

```
nodered-mcp/
├── cmd/nodered-mcp/
│   ├── main.go       # entrypoint; subcomandos serve / init / version
│   └── init.go       # detección e instalación de config en clientes MCP
├── internal/
│   ├── config/
│   │   ├── config.go     # loader de env vars + flags
│   │   └── httpauth.go   # bearer + OAuth wiring
│   ├── nodered/
│   │   ├── client.go     # HTTP client + auth
│   │   ├── flows.go      # CRUD de flows (JSON opaco)
│   │   ├── edit.go       # edición granular de nodos
│   │   ├── inspect.go    # resumen y búsqueda sobre flows
│   │   ├── diff.go       # comparación de configuraciones
│   │   ├── nodes.go      # palette: install / uninstall / enable / disable
│   │   ├── settings.go   # settings, estado, búsqueda npm
│   │   ├── runtime.go    # diagnóstico, contexto, plugins
│   │   ├── comms.go      # tail del WebSocket /comms
│   │   ├── backup.go     # snapshot antes de escribir
│   │   └── types.go      # tipos mínimos + RawFlow
│   ├── mcp/
│   │   ├── server.go     # stdio + streamable HTTP transports
│   │   ├── tools.go      # registra las 29 tools
│   │   ├── resources.go  # 3 resources
│   │   ├── prompts.go    # 2 prompts
│   │   └── httpauth.go   # bearer + OAuth middleware
│   └── oauth/
│       ├── discovery.go  # /.well-known/openid-configuration
│       ├── jwks.go       # JWKS cache + verificación
│       ├── verifier.go   # verificación de tokens
│       └── middleware.go # authorization middleware
└── examples/
    ├── claude_desktop_config.json
    ├── cursor_mcp.json
    ├── gemini_settings.json
    ├── vscode_mcp.json
    ├── opencode_config.json
    └── pi_mcp_config.json
```

**Dependencias:**

| Componente | Elección |
|---|---|
| Lenguaje | Go 1.25+ |
| SDK de MCP | `mark3labs/mcp-go` — stdio y streamable HTTP |
| Cliente HTTP | `net/http` (stdlib) |
| WebSocket | `coder/websocket` — stream de debug de `/comms` |
| JWT | `golang-jwt/jwt/v5` — verificación de tokens OAuth |
| Logging | `log/slog` (stdlib) |
| Configuración de desarrollo | `godotenv` |

Sin frameworks, sin ORMs, sin cliente Node-RED de terceros.

---

## 5. Catálogo de tools (29)

Marcadas por riesgo. `read` = sin efectos · `write` = muta config (backup
previo) · `action` = efecto en runtime no persistido. En el código, las
`action` también se registran como `addWriteTool` para que `--read-only` las
oculte.

Las 14 marcadas `read` son las únicas que se registran con `--read-only`.

### Flows

| Tool | HTTP | Tipo | Estado |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | ✅ resumen por defecto, `detail="full"` opcional |
| `search_flows` | `GET /flows` | read | ✅ |
| `get_flow` | `GET /flow/:id` | read | ✅ |
| `create_flow` | `POST /flow` | write | ✅ |
| `update_flow` | `PUT /flow/:id` | write | ✅ reescritura completa; preferir las granulares |
| `delete_flow` | `DELETE /flow/:id` | write | ✅ |
| `set_flows` | `POST /flows` | write | ✅ deploy completo, la más destructiva |
| `add_node` | `PUT /flow/:id` | write | ✅ |
| `update_node` | `PUT /flow/:id` | write | ✅ fusiona propiedades |
| `delete_node` | `PUT /flow/:id` | write | ✅ limpia wires entrantes |
| `connect_nodes` | `PUT /flow/:id` | write | ✅ |
| `inject_node` | `POST /inject/:id` | action | ✅ excluida de `--read-only` |

### Palette

| Tool | HTTP | Tipo | Estado |
|---|---|---|---|
| `list_nodes` | `GET /nodes` | read | ✅ |
| `get_node_info` | `GET /nodes/:module` | read | ✅ |
| `search_nodes` | registro npm | read | ✅ mirror privado vía `Options.SearchBaseURL` |
| `install_node` | `POST /nodes` | write | ✅ |
| `uninstall_node` | `DELETE /nodes/:module` | write | ✅ |
| `enable_node` | `PUT /nodes/:module[/:set]` | write | ✅ |
| `disable_node` | `PUT /nodes/:module[/:set]` | write | ✅ |

### Runtime, diagnóstico y recuperación

| Tool | HTTP | Tipo | Estado |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | ✅ |
| `get_diagnostics` | `GET /diagnostics` | read | ✅ requiere Node-RED ≥3.1 |
| `get_flows_state` | `GET /flows/state` | read | ✅ |
| `get_context` | `GET /context/...` | read | ✅ editor-api, sin contrato de estabilidad |
| `get_debug_messages` | WebSocket `/comms` | read | ✅ buffer de 500, reconexión |
| `list_plugins` | `GET /plugins` | read | ✅ editor-api |
| `set_flows_state` | `POST /flows/state` | write | ✅ |
| `list_backups` | local | read | ✅ |
| `diff_flows` | local + `GET /flows` | read | ✅ |
| `restore_backup` | `POST /flows` | write | ✅ |

### Resources (3)

| URI | Descripción |
|---|---|
| `nodered://flows/current` | Configuración de flows actual completa |
| `nodered://settings` | Ajustes del servidor |
| `nodered://flows/state` | Estado del runtime |

### Prompts (2)

| Nombre | Descripción |
|---|---|
| `explain_flow` | Describir qué hace un flow, sus disparadores y sus dependencias externas |
| `generate_flow` | Construir un flow a partir de una descripción en lenguaje natural |

---

## 5.b Hoja de fases

Plan trazado el 2026-07-27 tras auditar el proyecto. Las fases se ordenan
por dependencia y por valor, no por tamaño.

| Fase | Contenido | Estado |
|---|---|---|
| **0** | Publicar el repo y empujar un tag `vX.Y.Z` | ✅ Completada — `v0.5.1` |
| **1** | `--read-only`, `search_flows`, `list_flows` resumido | ✅ Completada |
| **2** | Envoltorio npm, imagen en GHCR, tap de Homebrew | ✅ Completada — npm `@fgjcarlos/nodered-mcp`, GHCR `ghcr.io/fgjcarlos/nodered-mcp` |
| **3** | `get_diagnostics`, `get_context`, `list_plugins` | ✅ Completada |
| **4** | `get_debug_messages` (WebSocket `/comms`) | ✅ Completada |
| **5** | Edición granular de nodos y `diff_flows` | ✅ Completada |
| **6** | Bearer auth en el transporte HTTP | ✅ Completada |
| **6.b** | OAuth 2.1 para conectores web | ✅ Completada — `internal/oauth/` |

Tras la fase 6: **29 tools** (14 en modo de solo lectura), 3 resources, 2
prompts.

### Detalle de lo entregado

- **Fase 1.** El modo de solo lectura se aplica en el *registro* de tools, no
  dentro de cada handler: las 15 que modifican no llegan a anunciarse, así
  que un modelo no puede invocar lo que no ve. `inject_node` cuenta como
  modificadora — disparar un inject puede mandar una orden a un dispositivo
  real. `search_flows` y el resumen de `list_flows` reducen 18x el contexto
  medido sobre una instancia real de 152 nodos.
- **Fase 3.** `get_context` es de solo lectura porque la admin API no expone
  escritura de contexto; no es una decisión de diseño nuestra. `/context` y
  `/plugins` pertenecen a la editor-api, no a la admin API documentada:
  funcionan, pero sin contrato de estabilidad.
- **Fase 4.** El tail arranca con el servidor y no con la primera llamada,
  porque un tail que empieza cuando preguntas se pierde justo lo que querías
  ver. Nunca es fatal: una instancia caída no impide arrancar ni bloquea las
  demás tools.
- **Fase 5.** `add_node`, `update_node`, `delete_node`, `connect_nodes` y
  `diff_flows`. `update_node` fusiona propiedades en lugar de reemplazar el
  nodo, así que los campos que este servidor no conoce sobreviven intactos.
  `delete_node` limpia además los wires que apuntaban al nodo eliminado.
  `connect_nodes` añade al puerto indicado y hace crecer el array si ese
  puerto aún no existía, en lugar de reescribirlo entero.

  **Descubrimiento durante la implementación:** `GET /flow/:id` **no** devuelve
  un único array `nodes`. Node-RED reparte el contenido del tab en `nodes` y
  `configs`, y la regla la decide `runtime/lib/flows/util.js`: un objeto con
  coordenadas `x` e `y` va a `nodes`, cualquier otro va a `configs`. Por eso
  un broker MQTT compartido vive en `configs` aunque pertenezca al tab. Las
  ediciones respetan ese reparto; ignorarlo hacía desaparecer nodos del
  canvas.

- **Fase 6.** El transporte HTTP **se niega a arrancar** en una dirección no
  loopback sin `MCP_HTTP_TOKEN`. Antes arrancaba sin autenticación. Ese
  cambio rompió un test existente que afirmaba el comportamiento antiguo; se
  actualizó porque codificaba un contrato que ya no queremos, no para que
  pasara.

- **Fase 6.b.** `nodered-mcp` actúa como **resource server** OAuth 2.1 / OIDC:
  no emite tokens, solo los verifica contra el issuer. En arranque, hace
  discovery una sola vez desde `<issuer>/.well-known/openid-configuration`
  y cachea el JWKS. Configurar `MCP_HTTP_TOKEN` y `MCP_OAUTH_ISSUER` a la
  vez es error de configuración y el servidor rehúsa arrancar.

---

## 6. Playbook de desarrollo (etapas)

Orden = dependencias. Cada etapa deja tests y es reviewable por separado.

- ✅ **Etapa 1 — Cimientos.** Flows y nodos como JSON opaco (`RawFlow`);
  `backup.go` con snapshot fail-closed antes de toda escritura.
- ✅ **Etapa 2 — Estrategia de edición.** `get_flow`, `create_flow`,
  `update_flow`, `delete_flow` con backup, más `validateFlowWires` antes del
  PUT. La pregunta *"¿flow entero vs. operaciones granulares?"* se respondió
  en la fase 5: **ambas**, con las granulares como camino por defecto y el
  flow entero como salida de emergencia.
- ✅ **Etapa 3 — Palette + acción.** `list_nodes`, `get_node_info`,
  `inject_node`, resources y prompts.
- ✅ **Etapa 4 — Rollback.** `list_backups`, `restore_backup`.
- ✅ **Etapa 5 — Deploy completo + runtime state.** `set_flows`,
  `set_flows_state`, `get_settings`, `get_flows_state`.
- ✅ **Etapa 6 — Gestión de palette.** install / uninstall / enable / disable,
  más `search_nodes`.
- ✅ **Etapa 7 — Tail del WebSocket `/comms`** y **transporte HTTP** con
  bearer auth. Entregada en la rama principal.
- ✅ **Etapa 8 — Diff de flows.** `diff_flows` contra un snapshot local.
- ✅ **Etapa 9 — OAuth 2.1.** Resource server con JWKS discovery.

Fuera de etapa, por necesidad detectada durante el trabajo:

- ✅ **CI** (no existía ninguno) — formato, vet, tests en 3 SO, race, build
  cruzado.
- ✅ **Sellado de versión** en `go install` — `resolveVersion` extrae la
  versión desde build info.
- ⬜ **Caché local de consultas** — sin planificar; ver §8.

---

## 7. Configuración

13 variables de entorno. Cada una admite también flag. Precedencia:
**flag > env > default.**

| Variable | Default | Flag | Descripción |
|---|---|---|---|
| `NODERED_URL` | `http://localhost:1880` | `--url` | URL base de Node-RED |
| `NODERED_TOKEN` | *(vacío)* | `--token` | Bearer token |
| `NODERED_USERNAME` | *(vacío)* | — | Basic auth (alternativa) |
| `NODERED_PASSWORD` | *(vacío)* | — | Basic auth (alternativa) |
| `NODERED_INSECURE` | `false` | — | Skip TLS verify |
| `NODERED_BACKUP_DIR` | `backups` | — | Dónde se guardan los snapshots |
| `MCP_LOG_LEVEL` | `info` | `--log-level` | debug / info / warn / error |
| `MCP_TRANSPORT` | `stdio` | `--transport` | `stdio` o `http` |
| `MCP_HTTP_ADDR` | `:8090` | `--http-addr` | Dirección de escucha del transporte http |
| `MCP_HTTP_TOKEN` | *(vacío)* | `--http-token` | Bearer token del transporte http. **Obligatorio** si la dirección no es loopback |
| `MCP_READ_ONLY` | `false` | `--read-only` | Registrar solo las 14 tools sin efectos secundarios |
| `MCP_DEBUG_STREAM` | `false` | `--debug-stream` | Abrir el WebSocket de `/comms` al arrancar para activar el stream de debug. **Desactivado por defecto** porque algunas versiones de Node-RED crashean durante el handshake (#17). Tras activarlo, `get_debug_messages` necesita ~3s para empezar a recibir mensajes |
| `MCP_OAUTH_ISSUER` | *(vacío)* | `--oauth-issuer` | Habilita OAuth 2.1 / OIDC en el transporte HTTP |
| `MCP_OAUTH_AUDIENCE` | *(vacío)* | `--oauth-aud` | Audience claim obligatorio cuando hay issuer |

`--http-addr :8090` **no** es loopback: escucha en todas las interfaces. Es
la razón de que el token sea obligatorio ahí.

`nodered-mcp version` imprime la versión. Si se instaló vía
`go install …@latest`, la versión visible es el tag `v0.5.1` porque
`resolveVersion` (`cmd/nodered-mcp/main.go:34`) la recupera del build info.

---

## 8. Riesgos & decisiones pendientes

| Tema | Estado |
|---|---|
| Modelo de datos con pérdida de campos | ✅ Resuelto: JSON opaco |
| Backup antes de escribir | ✅ Implementado, fail-closed |
| Estrategia de edición de flows por un LLM | ✅ Resuelta en la fase 5: granular por defecto, flow entero como salida de emergencia |
| WebSocket `/comms` | ✅ Implementado con reconexión y buffer acotado |
| HTTP transport además de stdio | ✅ Implementado |
| Bearer auth en el transporte HTTP | ✅ Token obligatorio en binds no-loopback; el servidor se niega a arrancar sin él |
| OAuth para clientes web | ✅ Resource server OAuth 2.1 / OIDC con JWKS discovery |
| **Concurrencia (dos LLMs editando)** | ⬜ **Sin resolver: last-write-wins.** Las ediciones granulares hacen read-modify-write, así que la ventana se estrecha pero no desaparece |
| **Poda de backups** | ⬜ **Pendiente** (marcado con `ponytail:` en `backup.go`). Se añade cuando el directorio moleste |
| Tamaño del buffer de debug | ⬜ Fijo en 500; configurable si alguien topa con el techo |
| Caché local de consultas | ⬜ Sin planificar; aparece en la fase 7 como "futuro" |

---

## 9. Prueba end-to-end

```bash
docker run -it -p 1880:1880 nodered/node-red      # 1. levantar Node-RED
go build -o nodered-mcp ./cmd/nodered-mcp          # 2. compilar
./nodered-mcp init --write                         # 3. configurar el cliente detectado
# 4. "Listame los flows de mi Node-RED" → el cliente invoca list_flows
```

Para el ciclo completo, que es lo que distingue a este servidor: pedirle
que cree un flow con un nodo debug, lo dispare con `inject_node` y lea
`get_debug_messages`.

Para el transporte HTTP con OAuth:

```bash
MCP_TRANSPORT=http \
MCP_HTTP_ADDR=:8090 \
MCP_OAUTH_ISSUER=https://your-idp.example/ \
MCP_OAUTH_AUDIENCE=nodered-mcp \
  ./nodered-mcp serve
```

---

## 10. Apéndice — verificabilidad

Esta sección existe para que un reviewer externo pueda confirmar cada
afirmación de las anteriores en una tarde.

### Una línea

29 tools, 3 resources, 2 prompts, ~90 tests, dos transportes, OAuth 2.1
opcional. El plan técnico está terminado.

### Cómo verificar la lista de tools

```bash
grep -oE 'mcp\.NewTool\("[a-z_]+"' internal/mcp/tools.go | sed 's/.*"//;s/"//' | sort -u
grep -c 'addReadTool('  internal/mcp/tools.go    # -> 14
grep -c 'addWriteTool(' internal/mcp/tools.go    # -> 15
```

### Cómo verificar el árbol de fuentes

```bash
find cmd internal -name '*.go' ! -name '*_test.go' | sort
```

El árbol de §4 debe coincidir exactamente. Cualquier `.go` no listado en
§4 es drift; cualquier ruta listada en §4 que no exista en disco también.

### Cómo verificar las variables de entorno

```bash
grep -ohE '"(NODERED|MCP)_[A-Z_]+"' internal/config/*.go | sort -u
```

El resultado debe coincidir con la lista en §7. `grep -c zerolog PLAN.md`
debe devolver `0`.

### Hallazgos que costaron tiempo

Anotados aquí porque no son evidentes leyendo el código y volverán a morder
a quien los ignore:

- **`GET /flow/:id` no devuelve un único array.** Reparte el contenido en
  `nodes` y `configs` según si el objeto lleva coordenadas `x`/`y`
  (`runtime/lib/flows/util.js`). Un broker compartido pertenece al tab pero
  aparece en `configs`.
- **`go install …@latest` ya funciona sin tag**, resolviendo una
  pseudo-versión. `resolveVersion` recupera la versión real desde build info.
- **`gofmt -l` marca todos los ficheros en Windows** por CRLF en el working
  tree. El repo guarda LF y el gate de CI pasa. No hace falta
  `.gitattributes`.
- **`go test -race` falla localmente** por un TDM-GCC roto, no por el código.
  Por eso CI ejecuta `-race` solo en Linux.
- **`:8090` no es loopback.** Escucha en todas las interfaces. Es la razón de
  que el token sea obligatorio ahí.
- **Configurar `MCP_HTTP_TOKEN` y `MCP_OAUTH_ISSUER` a la vez** es error de
  configuración y el servidor rehúsa arrancar. No es un runtime check — es
  una validación en el arranque.
