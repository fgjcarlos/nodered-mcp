# nodered-mcp — Plan & Playbook

> Un servidor **MCP (Model Context Protocol)** que expone la admin API de Node-RED
> como tools/resources/prompts para clientes IA (Claude Desktop, Cursor, Cline, etc.).
>
> Stack: **Go 1.25+**, single binary, stdio transport.

---

## 0. Estado actual (honesto)

| Capa | Estado |
|---|---|
| `internal/nodered` (cliente HTTP) | Métodos CRUD + inject + nodes implementados |
| `internal/mcp` (tools) | **Solo `list_flows` cableada.** Las otras 7 son `TODO` |
| `internal/config` | Completo (env vars + validación) |
| Modelo de datos | **Roto: pierde campos de nodos** (ver §2) |
| Backup antes de escribir | **No existe** (ver §3) |

El README/plan viejo prometía "8 tools disponibles" — eso hoy es falso. Este documento
corrige el rumbo: **primero cimientos, después tools.**

---

## 1. Por qué

Node-RED es programación visual de flows. Un MCP habilita un loop nuevo:

> *"Claude, listame los flows → agregá un nodo MQTT que escuche `home/temp` → deployá →
> mostrame los mensajes debug de los últimos 30s"*

…sin abrir el editor ni copiar JSON a mano.

---

## 2. Decisión de diseño clave: los flows son JSON OPACO

El modelo actual (`types.go`) tipa `Node` con campos fijos y un `Extra map[string]interface{}`
con tag `json:"-"`. **`json:"-"` hace que `encoding/json` ignore ese campo por completo**, así que
todo campo propio de un nodo (`topic`, `func`, `payload`, `repeat`, `broker`, `url`…) se **descarta**
en el unmarshal. Un round-trip `GET /flows` → `PUT /flow/:id` **vacía los nodos**. `update_flow`
no edita: mutila.

**Raíz:** Node-RED es deliberadamente *schemaless* — cada tipo de nodo tiene su propia forma.
Tipar structs sobre eso garantiza pérdida de datos.

**Regla nueva:**
- Flows y nodos se manejan como **JSON opaco** (`json.RawMessage` / `map[string]any`). **Cero pérdida de campos.**
- Solo se parsean los campos concretos que se necesitan **en el punto donde se necesitan**
  (ej: extraer `id`/`type`/`label` para un resumen legible).
- El server **nunca reescribe** un nodo que no entiende — lo pasa tal cual.
- Ojo con lo que un LLM edita a ciegas: `wires` (`[[out1,out2],...]`), IDs, **config nodes compartidos**
  (un `mqtt-broker` vive al top level, no dentro del nodo MQTT) y credenciales (canal aparte).
  La estrategia de edición se define en la Etapa 2, no se improvisa.

---

## 3. Seguridad: backup ANTES de cada escritura (guardrail de v0.1)

Toda operación mutante (`POST /flows`, `PUT /flow/:id`, `DELETE /flow/:id`) hace, **antes** de tocar
nada: `GET /flows` (el flows.json completo) y lo guarda en disco con timestamp.

- Dir: env `NODERED_BACKUP_DIR` (default `./backups` o temp del OS).
- Nombre: `flows-<RFC3339>.json`.
- Un snapshot del config completo cubre cualquier escritura ("al menos flows.json").
- Habilita rollback vía tools `list_backups` / `restore_backup` (Etapa 4).
- Node-RED tiene su propio `.backup` de un nivel, pero es local al host y el MCP habla por HTTP → no sirve.

<!-- ponytail: sin poda de backups en v0.1; agregar prune (keep last N) cuando el dir crezca -->

---

## 4. Arquitectura

```
MCP Client (Claude, Cursor…) ──JSON-RPC/stdio──▶ nodered-mcp (Go) ──HTTP──▶ Node-RED admin API :1880
```

```
nodered-mcp/
├── cmd/nodered-mcp/main.go     # entrypoint
├── internal/
│   ├── config/config.go        # env vars
│   ├── nodered/
│   │   ├── client.go           # HTTP client + auth
│   │   ├── flows.go            # CRUD (JSON opaco)
│   │   ├── nodes.go            # palette
│   │   ├── backup.go           # snapshot antes de escribir  ← NUEVO
│   │   └── types.go            # tipos mínimos + RawFlow
│   └── mcp/
│       ├── server.go
│       ├── tools.go            # registra tools
│       ├── resources.go
│       └── prompts.go
└── examples/claude_desktop_config.json
```

**Dependencias:** `mark3labs/mcp-go` (SDK MCP), `net/http` (stdlib), `godotenv`, `zerolog`.
Sin frameworks, sin ORMs, sin cliente Node-RED de terceros.

---

## 5. Catálogo de tools

Marcadas por riesgo. `read` = segura · `write` = muta config (exige backup) · `action` = side-effect no persistido.

### v0.1 — MVP (las 8 originales + guardrail de backup)

| Tool | HTTP | Tipo | Nota |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | ✅ ya existe |
| `get_flow` | `GET /flow/:id` | read | |
| `list_nodes` | `GET /nodes` | read | |
| `get_node_info` | `GET /nodes/:module` | read | |
| `create_flow` | `POST /flow` | write | backup antes |
| `update_flow` | `PUT /flow/:id` | write | backup antes · JSON opaco |
| `delete_flow` | `DELETE /flow/:id` | write | backup antes |
| `inject_node` | `POST /inject/:id` | action | dispara inject |

### Etapas siguientes (NO se implementan hasta que el uso las pida)

| Tool | HTTP | Tipo | Etapa |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | 5 |
| `get_flows_state` | `GET /flows/state` | read | 5 |
| `set_flows` | `POST /flows` | write | 5 · deploy completo (la más destructiva) |
| `set_flows_state` | `POST /flows/state` | write | 5 · start/stop runtime |
| `install_node` | `POST /nodes` | write | 6 |
| `remove_node` | `DELETE /nodes/:module` | write | 6 |
| `enable_node` / `disable_node` | `PUT /nodes/:module` | write | 6 |
| `list_backups` / `restore_backup` | (MCP-side) | write | 4 |

---

## 6. Playbook de desarrollo (etapas)

Orden = dependencias. Cada etapa deja tests y es reviewable por separado.

- **Etapa 1 — Cimientos (bloqueante).**
  - Arreglar modelo de datos: flows/nodos como JSON opaco (`RawFlow`), matar el `Extra json:"-"`.
  - Test de round-trip que falla si se pierde un solo campo de nodo.
  - `backup.go`: snapshot `GET /flows` antes de toda escritura.
- **Etapa 2 — Estrategia de edición.**
  - Definir cómo edita el LLM: ¿flow entero vs. operaciones granulares? ¿el server valida wires/refs antes del PUT?
  - Cablear `get_flow`, `create_flow`, `update_flow`, `delete_flow` con backup.
- **Etapa 3 — Palette + acción.** `list_nodes`, `get_node_info`, `inject_node`. Resources y prompts reales.
- **Etapa 4 — Rollback.** `list_backups`, `restore_backup`.
- **Etapa 5 — Deploy completo + runtime state.** `set_flows`, `set_flows_state`, `get_settings`, `get_flows_state`.
- **Etapa 6 — Gestión de palette.** install/remove/enable/disable nodes.
- **Etapa 7 (futuro).** WebSocket `/debug` tail, HTTP/SSE transport, diff de flows, cache local de queries.

---

## 7. Configuración

| Variable | Default | Descripción |
|---|---|---|
| `NODERED_URL` | `http://localhost:1880` | URL base |
| `NODERED_TOKEN` | *(vacío)* | Bearer token |
| `NODERED_USERNAME` / `NODERED_PASSWORD` | *(vacío)* | Basic auth (alternativa) |
| `NODERED_INSECURE` | `false` | Skip TLS verify |
| `NODERED_BACKUP_DIR` | `./backups` | Dónde se guardan los snapshots ← NUEVO |
| `MCP_LOG_LEVEL` | `info` | debug/info/warn/error |
| `MCP_TRANSPORT` | `stdio` | stdio por ahora |

---

## 8. Riesgos & decisiones pendientes

| Tema | Estado |
|---|---|
| Modelo de datos con pérdida de campos | **A arreglar en Etapa 1 (bloqueante)** |
| Backup antes de escribir | **Movido a v0.1 (Etapa 1)** |
| Estrategia de edición de flows por un LLM | **Pendiente — se decide en Etapa 2** |
| Concurrencia (dos LLMs editando) | MVP no lo maneja: last-write-wins |
| Poda de backups | Pendiente (agregar cuando el dir crezca) |
| WebSocket `/debug` | Etapa 7 |
| HTTP transport además de stdio | Etapa 7 |

---

## 9. Prueba end-to-end

```bash
docker run -it -p 1880:1880 nodered/node-red      # 1. levantar Node-RED
go build -o nodered-mcp ./cmd/nodered-mcp          # 2. compilar
# 3. configurar Claude Desktop (ver examples/claude_desktop_config.json)
# 4. "Listame los flows de mi Node-RED" → Claude invoca list_flows
```
