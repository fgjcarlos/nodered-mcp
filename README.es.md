# nodered-mcp

> Un servidor **MCP (Model Context Protocol)** en Go que expone la admin API de Node-RED como tools, resources y prompts para clientes IA (Claude Desktop, Cursor, Cline, etc.).

```
   Claude Desktop  ───stdin/stdout───▶  nodered-mcp  ───HTTP───▶  Node-RED :1880
```

## ¿Qué hace?

Le da a tu LLM la capacidad de:

- 📋 **Listar, leer, crear, actualizar y borrar** flows (`list_flows`, `get_flow`, `create_flow`, `update_flow`, `delete_flow`)
- ⚡ **Disparar nodos inject** manualmente sin abrir el editor (`inject_node`)
- 🧩 **Inspeccionar la palette**: qué nodos tenés instalados y qué hace cada uno (`list_nodes`, `get_node_info`)
- 📦 **Gestionar la palette**: instalar, desinstalar y habilitar/deshabilitar módulos de nodos (`install_node`, `uninstall_node`, `enable_node`, `disable_node`)
- 🔎 **Buscar módulos en el catálogo npm** antes de instalar (`search_nodes`)
- ⚙️ **Diagnosticar el server**: leer la config y el estado del runtime (`get_settings`, `get_flows_state`, `set_flows_state`, `set_flows`)
- 🛟 **Recuperar cambios**: backups automáticos antes de cada escritura + `list_backups` / `restore_backup`
- 🧠 **Recibir prompts reusables** (`explain_flow`, `generate_flow`) que arrancan con el contexto correcto

Más detalles en [`PLAN.md`](./PLAN.md).

## Instalación

> **nodered-mcp es un servidor MCP genérico.** No depende de ningún proveedor de IA: el mismo binario funciona con cualquier cliente que hable MCP — Claude, Cursor (con GPT o el modelo que uses), Gemini CLI, VS Code, Cline… Elegí un método de instalación y después [conectá tu cliente](#conectar-tu-cliente-mcp).

Funciona en **Linux, macOS y Windows** (amd64 y arm64).

### Opción A — Binario precompilado (recomendado, sin Go)

Descargá el binario de tu sistema desde [Releases](https://github.com/fgjcarlos/nodered-mcp/releases) y ponelo en el PATH.

Linux / macOS:

```bash
# ajustá el nombre del archivo a tu SO/arquitectura
curl -sSL https://github.com/fgjcarlos/nodered-mcp/releases/latest/download/nodered-mcp_Linux_x86_64.tar.gz | tar xz
sudo mv nodered-mcp /usr/local/bin/
nodered-mcp version
```

Windows (PowerShell): descargá el `.zip`, descomprimilo y movés `nodered-mcp.exe` a una carpeta que esté en el `PATH`.

### Opción B — Docker (ideal para el transporte HTTP)

```bash
docker build -t nodered-mcp .
docker run --rm -p 8090:8090 \
  -e NODERED_URL=http://host.docker.internal:1880 \
  -e NODERED_TOKEN=tu-token \
  nodered-mcp
# endpoint MCP: http://localhost:8090/mcp
```

En Linux, si Node-RED corre en el host, añadí `--add-host=host.docker.internal:host-gateway` al `docker run`. La imagen arranca en transporte **http** por defecto (stdio no tiene sentido dentro de un contenedor).

### Opción C — `go install` (si ya tenés Go)

```bash
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
# queda en $(go env GOPATH)/bin — verificá que esté en tu PATH
```

### Opción D — Desde el código

```bash
git clone https://github.com/fgjcarlos/nodered-mcp
cd nodered-mcp
go build -o nodered-mcp ./cmd/nodered-mcp
```

> **Estado actual:** las opciones B y D funcionan hoy. Las opciones A y C requieren que el repo esté publicado en GitHub con al menos un tag `vX.Y.Z` (ver [`issues/301`](./issues/301-publish-repo.md)). Hasta entonces, usá Docker o compilá desde el código.

## Configuración

Toda la config se puede pasar por env vars o por flags. Las flags ganan sobre las env vars, y estas sobre los defaults.

```bash
cp .env.example .env
# editá .env
```

| Variable | Flag | Default | Descripción |
|---|---|---|---|
| `NODERED_URL` | `--url` | `http://localhost:1880` | URL de Node-RED |
| `NODERED_TOKEN` | `--token` | — | Bearer token si tenés admin auth activo |
| `NODERED_USERNAME` / `NODERED_PASSWORD` | — | — | Basic auth como alternativa al token |
| `NODERED_INSECURE` | — | `false` | Aceptar TLS sin verificar (solo dev) |
| `MCP_TRANSPORT` | `--transport` | `stdio` | `stdio` o `http` |
| `MCP_HTTP_ADDR` | `--http-addr` | `:8090` | Dirección de escucha del transporte `http` |
| `MCP_LOG_LEVEL` | `--log-level` | `info` | `debug` \| `info` \| `warn` \| `error` |

### CLI

```bash
nodered-mcp                # arranca el server (equivale a `nodered-mcp serve`)
nodered-mcp serve --help   # lista todas las flags
nodered-mcp init           # genera el snippet de config para tu cliente MCP
nodered-mcp version        # imprime la versión
```

`init` detecta qué clientes MCP tenés instalados, te pregunta la URL/puerto de Node-RED, el token y la carpeta de backups, y **autodetecta la ruta del binario** — así el snippet nunca apunta a un ejecutable inexistente (la causa típica de "Server disconnected").

- `nodered-mcp init` → imprime el snippet para pegar.
- `nodered-mcp init --write` → lo **escribe directo** en la config del cliente (merge seguro que preserva tus otros servidores; guarda un `.bak` del archivo previo). Soportado para Claude Desktop, Cursor y Gemini CLI; para VS Code (config por workspace) y Claude Code (`claude mcp add`) imprime la instrucción.
- `nodered-mcp init --all` → muestra todos los clientes, no solo los detectados.

## Transportes

- **stdio** (default): el cliente MCP lanza el binario y habla por stdin/stdout. Es lo que usan Claude Desktop, Cursor, VS Code, etc.
- **http** (streamable HTTP): un único proceso sirve a varios clientes remotos. El endpoint MCP queda en `<addr>/mcp`.

```bash
nodered-mcp serve --transport http --http-addr :8090
# endpoint: http://localhost:8090/mcp
```

> El transporte http no lleva auth propia todavía. No lo expongas fuera de localhost / red de confianza.

## Conectar tu cliente MCP

Todos los ejemplos usan **stdio**. Para HTTP, mirá la [variante HTTP](#variante-http) abajo.

### Claude Desktop

**Opción de un clic (recomendada): extensión `.mcpb`.** Es un instalador nativo — no editás JSON. Generá el bundle con `scripts/build-mcpb.sh` (o descargalo de Releases cuando esté publicado), y en Claude Desktop andá a **Settings → Extensions → Install Extension** y elegí el `.mcpb`. Claude Desktop te muestra un formulario pidiendo la URL/puerto de Node-RED, el token y la carpeta de backups. El token se guarda cifrado en el Credential Manager de Windows.

```bash
# genera nodered-mcp-<os>-<arch>.mcpb (necesita go + npx)
VERSION=v0.4.0 bash scripts/build-mcpb.sh
```

**Opción manual: editar el config.** `%APPDATA%\Claude\claude_desktop_config.json` en Windows (o su equivalente en tu OS). Ver [`examples/claude_desktop_config.json`](./examples/claude_desktop_config.json):

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-tenes"
      }
    }
  }
}
```

### Claude Code

```bash
claude mcp add nodered -e NODERED_URL=http://localhost:1880 -e NODERED_TOKEN=tu-token -- nodered-mcp
```

### VS Code

`.vscode/mcp.json` en tu workspace. Ver [`examples/vscode_mcp.json`](./examples/vscode_mcp.json):

```json
{
  "servers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-tenes"
      }
    }
  }
}
```

### Cursor

`.cursor/mcp.json` en tu workspace (o `~/.cursor/mcp.json` global). Ver [`examples/cursor_mcp.json`](./examples/cursor_mcp.json):

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-tenes"
      }
    }
  }
}
```

### Gemini CLI

`~/.gemini/settings.json`. Ver [`examples/gemini_settings.json`](./examples/gemini_settings.json):

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-tenes"
      }
    }
  }
}
```

### Variante HTTP

Arrancá el server una vez (`nodered-mcp serve --transport http --http-addr :8090`) y apuntá el cliente al endpoint en lugar de a un comando. En clientes que soportan `url`/`type: http`:

```json
{
  "mcpServers": {
    "nodered": { "url": "http://localhost:8090/mcp" }
  }
}
```

Tras conectar, reiniciá el cliente. Vas a ver las 19 tools disponibles.

## Troubleshooting

- **No aparecen las tools:** revisá que el binario esté en el `PATH`, o usá la ruta absoluta en `command`. En Windows escapá las barras (`C:\\ruta\\nodered-mcp.exe`).
- **401 / 403 desde Node-RED:** falta token o le falta scope. Con `adminAuth` activo, generá un token con permisos de escritura de flows.
- **El server no loguea nada:** los logs van a **stderr** (stdout está reservado para el protocolo). Subí el detalle con `--log-level debug`.
- **HTTP no conecta:** confirmá que `--transport http` está activo y que el puerto de `--http-addr` no esté ocupado.

## Ejemplo de uso

Una vez conectado, en Claude Desktop:

> **Vos:** "Listame los flows que tengo en Node-RED."
> **Claude:** *(invoca `list_flows`)* "Tenés 3 flows: `Home`, `MQTT Bridge` y `Weather`. ¿Querés que abra alguno?"

> **Vos:** "Agregame un nodo inject al flow `Home` que dispare cada 5 segundos con payload `hello`."
> **Claude:** *(lee el flow, propone el cambio, lo aplica con `update_flow`)*

## Arquitectura

```
cmd/nodered-mcp/        # entrypoint
internal/config/        # carga env vars
internal/nodered/       # cliente HTTP contra la admin API
internal/mcp/           # server MCP (tools, resources, prompts)
```

Stack:
- Go 1.25+
- [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — SDK de MCP (stdio + streamable HTTP)
- `net/http` (stdlib) — cliente Node-RED
- `log/slog` (stdlib) — logging
- [`godotenv`](https://github.com/joho/godotenv) — `.env` en dev

## Tests

```bash
go test ./...
```

## Roadmap

- **v0.1:** 10 tools, 1 resource, 2 prompts, stdio
- **v0.2:** transporte HTTP (streamable) + CLI con flags ✅
- **v0.3:** install/uninstall/enable/disable de nodos ✅
- **v0.4:** search_nodes + get/set settings + flows state (19 tools, 3 resources, 2 prompts) ✅
- **v0.5:** auth bearer en el transporte HTTP

Ver [`PLAN.md`](./PLAN.md) para el detalle completo.

## Licencia

MIT

## Repo hermano

[`nrcc`](https://github.com/fgjcarlos/nrcc) — Node-RED Control Center, un dashboard web en Go + React para administrar instancias de Node-RED. Es un proyecto aparte, no comparten código, pero están pensados para convivir.
