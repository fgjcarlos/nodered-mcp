# nodered-mcp

Un servidor [MCP (Model Context Protocol)](https://modelcontextprotocol.io)
escrito en Go que expone la admin API de Node-RED a clientes de IA
como tools, resources y prompts.

```mermaid
flowchart LR
  client["Cliente MCP<br/>(Claude, Cursor, …)"]
  server["nodered-mcp<br/>(este binario)"]
  nr["Node-RED :1880"]
  client -- "stdio / HTTP" --> server
  server -- "HTTP" --> nr
```

`nodered-mcp` es independiente del proveedor. El mismo binario
funciona con cualquier cliente compatible con MCP — Claude Desktop,
Claude Code, Cursor, VS Code, Gemini CLI, OpenCode, Pi, Cline — sea
cual sea el modelo subyacente.

La versión en inglés de este documento está en
[`README.md`](./README.md).

## Instalación

Tres canales soportados. Elige el que mejor te encaje:

```bash
# npm — funciona en todas las plataformas. Recomendado.
npm install -g @fgjcarlos/nodered-mcp
```

```bash
# go install — para quien tenga toolchain de Go.
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
```

```bash
# Docker — el binario corre dentro de la imagen; reinicia reemplazando el contenedor.
docker pull ghcr.io/fgjcarlos/nodered-mcp:latest
```

Tras instalar, genera el snippet para tu cliente MCP:

```bash
# 2. Generar el snippet para tu cliente MCP
nodered-mcp init --write

# 3. Reinicia tu cliente MCP; aparecen 43 tools bajo el servidor "nodered"
```

¿Problemas? Consulta [`docs/troubleshooting.md`](./docs/troubleshooting.md).
## Documentación

La referencia completa está en [`docs/`](./docs/):

| Doc | Cubre |
|---|---|
| [`docs/architecture.md`](./docs/architecture.md) | Árbol de fuentes, dependencias, modelo JSON-opaco, backup antes de escribir |
| [`docs/tools.md`](./docs/tools.md) | Catálogo de las 43 tools MCP (read / write / action) |
| [`docs/configuration.md`](./docs/configuration.md) | Variables de entorno y flags de línea de comandos |
| [`docs/transports.md`](./docs/transports.md) | Transportes stdio y streamable HTTP, bearer auth, OAuth 2.1 |
| [`docs/clients.md`](./docs/clients.md) | Snippets de configuración por cliente MCP |
| [`docs/troubleshooting.md`](./docs/troubleshooting.md) | Modos de fallo habituales y cómo recuperarse |
| [`docs/roadmap.md`](./docs/roadmap.md) | Trabajo abierto, riesgos aceptados, versiones planeadas |

## Seguridad

Dar acceso de escritura a un LLM sobre un runtime de automatización
en marcha exige guardarraíles. Hay tres integrados:

- Los documentos de flow se tratan como JSON opaco — sin structs
  fijos de Go, sin pérdida de campos.
- Cada operación mutante hace un snapshot completo de la
  configuración antes y falla en cerrado si no puede escribirlo.
- Los wires se validan antes de que la escritura llegue al runtime;
  los destinos colgantes se rechazan en la capa MCP.

Arranca con `--read-only` (o `MCP_READ_ONLY=true`) para anunciar
solo las 20 tools de lectura y esconder todas las mutantes en el
registro. `inject_node` también se esconde: disparar un inject puede
mandar una orden real a un dispositivo real.

Modelo de amenaza completo y checklist de hardening:
[`SECURITY.md`](./SECURITY.md).

## Desarrollo

```bash
go test ./...
go build -o nodered-mcp ./cmd/nodered-mcp
```

Las tareas viven como GitHub Issues. Las decisiones de diseño y
auditorías históricas están bajo [`docs/`](./docs/). Consulta
[`CONTRIBUTING.md`](./CONTRIBUTING.md) para el flujo de trabajo.

## Licencia

MIT. Ver [`LICENSE`](./LICENSE).
