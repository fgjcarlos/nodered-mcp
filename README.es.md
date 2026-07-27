# nodered-mcp

Un servidor [MCP (Model Context Protocol)](https://modelcontextprotocol.io) escrito en Go que expone la admin API de Node-RED a clientes de IA como tools, resources y prompts.

```
Cliente MCP  ──stdio | HTTP──▶  nodered-mcp  ──HTTP──▶  Node-RED :1880
```

`nodered-mcp` es independiente del proveedor. El mismo binario funciona con cualquier cliente compatible con MCP — Claude Desktop, Claude Code, Cursor, VS Code, Gemini CLI, OpenCode, Pi, Cline — sea cual sea el modelo subyacente.

La versión en inglés de este documento está en [`README.md`](./README.md).

## Contenido

- [Capacidades](#capacidades)
- [Modelo de seguridad](#modelo-de-seguridad)
- [Requisitos](#requisitos)
- [Instalación](#instalación)
- [Configuración](#configuración)
- [Línea de comandos](#línea-de-comandos)
- [Transportes](#transportes)
- [Integración con clientes](#integración-con-clientes)
- [Resolución de problemas](#resolución-de-problemas)
- [Arquitectura](#arquitectura)
- [Desarrollo](#desarrollo)
- [Hoja de ruta](#hoja-de-ruta)

## Capacidades

29 tools, 3 resources y 2 prompts. Las tools se clasifican por riesgo: **read** no tiene efectos secundarios, **write** modifica configuración persistida y realiza un backup previo, **action** tiene un efecto en el runtime que no se persiste.

### Flows

| Tool | Endpoint | Riesgo | Descripción |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | Mapa de pestañas, subflows, recuento de nodos y tipos presentes |
| `search_flows` | `GET /flows` | read | Localizar nodos en cualquier flow por texto libre o por tipo |
| `get_flow` | `GET /flow/:id` | read | Una pestaña de flow por su ID |
| `create_flow` | `POST /flow` | write | Crear una pestaña nueva |
| `update_flow` | `PUT /flow/:id` | write | Reemplazar una pestaña existente |
| `delete_flow` | `DELETE /flow/:id` | write | Eliminar una pestaña y sus nodos |
| `set_flows` | `POST /flows` | write | Despliegue completo: reemplaza toda la configuración |
| `add_node` | `PUT /flow/:id` | write | Añadir un nodo sin tocar los demás |
| `update_node` | `PUT /flow/:id` | write | Cambiar propiedades de un nodo, fusionando en vez de reemplazar |
| `delete_node` | `PUT /flow/:id` | write | Eliminar un nodo y los wires que apuntan a él |
| `connect_nodes` | `PUT /flow/:id` | write | Conectar la salida de un nodo con otro |
| `inject_node` | `POST /inject/:id` | action | Disparar un nodo inject bajo demanda |

### Palette

| Tool | Endpoint | Riesgo | Descripción |
|---|---|---|---|
| `list_nodes` | `GET /nodes` | read | Módulos instalados, versiones y estado de activación |
| `get_node_info` | `GET /nodes/:module` | read | Metadatos de un módulo instalado |
| `search_nodes` | registro npm | read | Buscar en el catálogo público antes de instalar |
| `install_node` | `POST /nodes` | write | Instalar un módulo desde npm |
| `uninstall_node` | `DELETE /nodes/:module` | write | Eliminar un módulo instalado |
| `enable_node` | `PUT /nodes/:module[/:set]` | write | Activar un módulo o uno de sus node sets |
| `disable_node` | `PUT /nodes/:module[/:set]` | write | Desactivar sin desinstalar |

### Runtime y recuperación

| Tool | Endpoint | Riesgo | Descripción |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | Configuración del servidor: esquema de auth, puerto, tema, plugins |
| `get_diagnostics` | `GET /diagnostics` | read | Versión de Node.js y memoria, sistema operativo, detección de contenedor |
| `get_flows_state` | `GET /flows/state` | read | Si el runtime está arrancado o detenido |
| `get_context` | `GET /context/...` | read | Estado que los flows conservan entre mensajes |
| `get_debug_messages` | WebSocket `/comms` | read | Lo que los flows produjeron realmente |
| `list_plugins` | `GET /plugins` | read | Plugins de editor cargados por el runtime |
| `set_flows_state` | `POST /flows/state` | write | Arrancar o detener el runtime |
| `list_backups` | local | read | Snapshots guardados, del más reciente al más antiguo |
| `diff_flows` | local + `GET /flows` | read | Qué cambió entre un snapshot y ahora |
| `restore_backup` | `POST /flows` | write | Revertir toda la configuración a un snapshot |

### Trabajar con instancias grandes

La configuración completa de flows de una instancia real es demasiado grande para entregársela íntegra a un modelo. Un montaje de 150 nodos ronda los 30.000 caracteres; unos cuantos cientos agotan la ventana de contexto antes de poder empezar a trabajar.

Hay dos tools para evitarlo.

`list_flows` devuelve por defecto un mapa compacto — pestañas, subflows, recuento de nodos y desglose por tipo — sin los cuerpos de los nodos. En esa misma instancia de 150 nodos el resumen ocupa unos 1.600 caracteres frente a los 30.000 del documento completo. Usa `detail="full"` cuando realmente necesites todo.

`search_flows` localiza nodos sin descargar la configuración. La consulta de texto se compara sin distinguir mayúsculas contra el JSON completo de cada nodo, de modo que encuentra valores alojados en campos propios del tipo de nodo — un topic MQTT, una url, el nombre de un nodo, una línea dentro del cuerpo de una función — sin que este servidor necesite conocer ningún tipo de nodo. Cada resultado incluye el nodo tal cual y la pestaña a la que pertenece, que es justo lo que necesita una llamada posterior a `get_flow` o `update_flow`.

```
search_flows(query: "sensors/room13/temperature")   -> 4 nodos, en 4 pestañas
search_flows(type: "function", limit: 3)            -> 16 coincidencias, se muestran 3
```

Cuando los resultados se truncan, la respuesta indica el total real, así que una lista recortada nunca se confunde con una completa.

### Diagnosticar el comportamiento, no solo la estructura

Leer los flows dice lo que una instancia *debería* hacer. Tres tools cubren por qué puede no estar haciéndolo.

`get_diagnostics` responde en una sola llamada a "sobre qué se está ejecutando esto realmente": versión de Node.js y memoria, sistema operativo, si vive en un contenedor, locale y zona horaria. Requiere Node-RED 3.1 o posterior; en versiones anteriores la tool lo dice en lugar de devolver un 404 sin explicación.

`get_context` lee el estado que los flows conservan entre mensajes. El contexto no aparece en ninguna parte del JSON de los flows, así que un flow puede parecer completamente correcto y aun así comportarse mal por un valor que guardó antes — esta es la única forma de verlo desde fuera del editor. El scope `global` es de toda la instancia; `flow` y `node` reciben el id de la pestaña o del nodo. Omite `key` para leer el almacén completo.

```
get_context(scope: "global")                      -> {"memory":{"temperature":{"msg":"21.5","format":"number"}}}
get_context(scope: "global", key: "temperature")  -> {"msg":"21.5","format":"number"}
get_context(scope: "flow", id: "tabA")            -> {"memory":{"counter":{"msg":"7","format":"number"}}}
```

Aquí el contexto es de solo lectura porque la admin API no expone ninguna forma de escribirlo: no hay un setter oculto que este servidor decida no ofrecer.

`list_plugins` lista los plugins de editor, que extienden el editor en lugar de añadir nodos y por eso nunca aparecen en `list_nodes`.

### Editar un nodo en vez de una pestaña entera

`update_flow` reemplaza una pestaña completa, lo que obliga al modelo a reproducir cada nodo exactamente. Todo lo que no reproduzca se destruye — y los nodos de Node-RED llevan campos propios de su tipo que ningún esquema conoce.

Las tools granulares lo evitan. Cada una lee la pestaña, cambia solo lo pedido y la escribe de vuelta con las mismas salvaguardas: wires validados y backup previo.

```
add_node(flow_id, node)                      -> añade y deja el resto byte a byte igual
update_node(flow_id, node_id, properties)    -> fusiona las claves dadas, conserva las demás
delete_node(flow_id, node_id)                -> lo elimina y limpia los wires entrantes
connect_nodes(flow_id, from_id, to_id, port) -> añade a ese puerto de salida
```

`update_node` fusiona en lugar de reemplazar: cambiar un topic MQTT deja intactos la referencia al broker, el QoS y la posición. `delete_node` también quita los wires que apuntaban al nodo, porque Node-RED acepta wires hacia la nada y simplemente no entrega — una pestaña que parece intacta y en silencio hace menos de lo que debería. `connect_nodes` añade al puerto indicado y hace crecer el array cuando ese puerto aún no existe, en vez de reescribirlo a mano.

Con guardas: se rechaza un id duplicado, no se puede cambiar el id de un nodo (los wires lo referencian) y se rehúsa conectar con un nodo inexistente.

Un detalle útil al leer una pestaña: Node-RED reparte su contenido entre `nodes` y `configs`, y decide según si el objeto lleva coordenadas `x`/`y`. Un broker MQTT compartido pertenece a la pestaña pero aparece en `configs`. Estas tools respetan ese reparto, así que los nodos de configuración también se pueden editar y nunca acaban archivados en el sitio equivocado.

`diff_flows` compara dos configuraciones cualesquiera — un backup contra la instancia viva, o dos backups — e informa de qué se añadió, se eliminó o cambió. Como se toma un backup antes de cada escritura, `diff_flows(from: "latest")` responde a "qué hizo realmente ese último cambio".

### Cerrar el ciclo: ver qué hizo un flow

Todas las demás tools describen la instancia. `get_debug_messages` informa de lo que realmente produjo: la salida de los nodos debug, tal como la muestra la barra lateral del editor.

Eso completa el ciclo que un modelo necesita para trabajar sin supervisión:

```
create_flow / update_flow  ->  inject_node  ->  get_debug_messages  ->  corregir y repetir
```

Sin el último paso, un modelo puede desplegar un flow y no llegar a saber nunca si funcionó.

Node-RED publica esto únicamente por el WebSocket `/comms` del editor; no existe endpoint HTTP para ello. Por eso `nodered-mcp` abre esa conexión al arrancar y mantiene un buffer circular con los 500 mensajes más recientes, de modo que la salida producida *antes* de que se te ocurriera preguntar ya está capturada. Usa `since` (marca de tiempo RFC 3339) para ver solo lo que llegó después de un instante dado, normalmente justo antes de inyectar.

La conexión se mantiene en segundo plano y nunca es fatal, a propósito:

- Si Node-RED no está accesible, el servidor arranca igual y el resto de tools funcionan.
- Un redespliegue o un reinicio tumban el runtime; el tail se reconecta con backoff exponencial acotado.
- Un resultado vacío distingue entre "sin conexión", "conectado pero no ha llegado nada" y "nada coincide con tu filtro": el silencio nunca es ambiguo.
- Si el buffer se desborda, la respuesta indica cuántos mensajes antiguos se descartaron.

Con `adminAuth` activo, el mismo token autentica el WebSocket. Necesita el permiso `status.read`; sin él Node-RED rechaza el handshake y el motivo aparece en la salida de la tool.

### Resources

| URI | Descripción |
|---|---|
| `nodered://flows/current` | Configuración de flows actual completa |
| `nodered://settings` | Ajustes del servidor |
| `nodered://flows/state` | Estado del runtime |

### Prompts

| Nombre | Descripción |
|---|---|
| `explain_flow` | Describir qué hace un flow, sus disparadores y sus dependencias externas |
| `generate_flow` | Construir un flow a partir de una descripción en lenguaje natural |

## Modelo de seguridad

Dar acceso de escritura a un LLM sobre un runtime de automatización en marcha exige salvaguardas. Hay tres integradas.

**Los documentos de flow se tratan como JSON opaco.** El modelo de nodos de Node-RED es deliberadamente schemaless: un nodo MQTT lleva `topic` y `broker`, un nodo function lleva `func`, un nodo inject lleva `payload` y `repeat`. Modelar eso con structs de Go fijos descartaría en silencio cada campo no reconocido en un ciclo de lectura y escritura. `nodered-mcp` reenvía el JSON de los flows tal cual y solo interpreta los campos concretos que necesita, en el punto donde los necesita. No se pierde ningún campo.

**Toda operación de escritura hace un backup previo y falla en cerrado.** Antes de cualquier escritura, el servidor descarga la configuración completa de flows y la guarda en un fichero con marca temporal bajo `NODERED_BACKUP_DIR`. Si el snapshot no se puede escribir — Node-RED inaccesible, directorio sin permisos — la escritura se aborta en lugar de arriesgar un cambio irrecuperable. `list_backups` y `restore_backup` exponen la vía de reversión.

**Los wires se validan antes de que la escritura llegue al runtime.** Node-RED acepta destinos de wire inexistentes sin protestar, dejando conexiones rotas. `create_flow` y `update_flow` rechazan cualquier documento cuyos wires apunten a un nodo que no existe dentro de él, lo que detecta el fallo más habitual del JSON de flows generado por un LLM.

Los nombres de backup están restringidos a nombres de fichero sin ruta, de modo que `restore_backup` no puede usarse para leer ficheros arbitrarios del disco.

### Modo de solo lectura

Para una instancia de producción, la salvaguarda más sólida es no ofrecer siquiera las tools peligrosas.

```bash
nodered-mcp serve --read-only        # o MCP_READ_ONLY=true
```

El servidor registra entonces únicamente las 14 tools sin efectos secundarios. Las 15 que modifican no llegan a anunciarse, de modo que un modelo no puede invocar lo que no ve: la restricción se aplica en el registro, no mediante una comprobación dentro de cada handler.

`inject_node` se considera modificadora y queda excluida. No escribe configuración, pero disparar un inject puede enviar una orden real a un dispositivo real.

Los resources y los prompts siguen disponibles: los tres resources son vistas de solo lectura y los prompts son texto inerte.

| Modo | Tools | Resources | Prompts |
|---|---|---|---|
| por defecto | 29 | 3 | 2 |
| `--read-only` | 14 | 3 | 2 |

## Requisitos

- Una instancia de Node-RED accesible (1.x o posterior) con su admin API habilitada.
- Nada más en tiempo de ejecución: `nodered-mcp` es un único binario estático.
- Go 1.25+ solo si compilas desde el código fuente.

Funciona en Linux, macOS y Windows (amd64 y arm64).

## Instalación

Elige un método y después [conecta tu cliente](#integración-con-clientes). Las cinco opciones instalan el mismo binario; la diferencia es quién gestiona la plataforma y arquitectura.

### Opción A — npm

El wrapper está publicado como [`@fgjcarlos/nodered-mcp`](https://www.npmjs.com/package/@fgjcarlos/nodered-mcp). En el `npm install` se descarga el binario correspondiente a tu plataforma desde el release de GitHub, y un shim lo reejecuta.

```bash
npm install -g @fgjcarlos/nodered-mcp
nodered-mcp version   # -> 0.5.9
```

Funciona en Linux, macOS y Windows (amd64 y arm64) sin configuración adicional. El wrapper no tiene dependencias npm — un parser POSIX de tar implementado a mano en `bin/tar.js` extrae el binario, así que `npm audit` solo ve el wrapper en sí.

### Opción B — Binario precompilado

Descarga el archivo correspondiente a tu plataforma desde [Releases](https://github.com/fgjcarlos/nodered-mcp/releases/latest) y coloca el binario en el `PATH`.

```bash
# ajusta el nombre del fichero a tu sistema y arquitectura
curl -sSL https://github.com/fgjcarlos/nodered-mcp/releases/latest/download/nodered-mcp_Linux_x86_64.tar.gz | tar xz
sudo mv nodered-mcp /usr/local/bin/
nodered-mcp version
```

En Windows, descarga el `.zip`, descomprímelo y mueve `nodered-mcp.exe` a un directorio incluido en el `PATH`.

### Opción C — go install

```bash
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
```

El binario queda en `$(go env GOPATH)/bin`; comprueba que ese directorio esté en el `PATH`.

`@latest` resuelve al tag más reciente. `nodered-mcp version` informa de lo que realmente se instaló: `go install` no aplica flags de enlazado, así que la versión se recupera de la información de módulo que incrusta el toolchain.

### Opción D — Docker

La imagen se publica automáticamente en GitHub Container Registry con cada release etiquetado.

```bash
docker pull ghcr.io/fgjcarlos/nodered-mcp:latest
docker run --rm -p 8090:8090 \
  -e NODERED_URL=http://host.docker.internal:1880 \
  -e NODERED_TOKEN=tu-token \
  ghcr.io/fgjcarlos/nodered-mcp:latest
```

El endpoint MCP queda en `http://localhost:8090/mcp`. La imagen usa el transporte HTTP por defecto, ya que stdio no tiene sentido dentro de un contenedor. En Linux, si Node-RED se ejecuta en el host, añade `--add-host=host.docker.internal:host-gateway`.

Para construir la imagen tú mismo desde el código fuente:

```bash
git clone https://github.com/fgjcarlos/nodered-mcp
cd nodered-mcp
docker build -t nodered-mcp .
```

### Opción E — Desde el código fuente

```bash
git clone https://github.com/fgjcarlos/nodered-mcp
cd nodered-mcp
go build -o nodered-mcp ./cmd/nodered-mcp
```

## Configuración

Cada ajuste puede indicarse como variable de entorno o como flag de línea de comandos. La precedencia es flag, después variable de entorno, después valor por defecto.

| Variable | Flag | Por defecto | Descripción |
|---|---|---|---|
| `NODERED_URL` | `--url` | `http://localhost:1880` | URL base de Node-RED |
| `NODERED_TOKEN` | `--token` | — | Bearer token, cuando la admin auth está activa |
| `NODERED_USERNAME` | — | — | Usuario de basic auth, como alternativa al token |
| `NODERED_PASSWORD` | — | — | Contraseña de basic auth |
| `NODERED_INSECURE` | — | `false` | Omitir la verificación TLS. Solo para desarrollo |
| `NODERED_BACKUP_DIR` | — | `backups` | Dónde se escriben los snapshots antes de cada escritura |
| `MCP_READ_ONLY` | `--read-only` | `false` | Exponer solo las tools que no pueden modificar Node-RED |
| `MCP_DEBUG_STREAM` | `--debug-stream` | `false` | Abrir el WebSocket de `/comms` al arrancar para activar el stream de debug. **Desactivado por defecto** porque algunas versiones de Node-RED crashean durante el handshake |
| `MCP_TRANSPORT` | `--transport` | `stdio` | `stdio` o `http` |
| `MCP_HTTP_ADDR` | `--http-addr` | `:8090` | Dirección de escucha del transporte HTTP |
| `MCP_HTTP_TOKEN` | `--http-token` | — | Bearer token del transporte HTTP. Obligatorio salvo con bind a loopback |
| `MCP_LOG_LEVEL` | `--log-level` | `info` | `debug`, `info`, `warn` o `error` |

Para desarrollo local, un fichero `.env` en el directorio de trabajo se carga automáticamente. Las variables de entorno ya definidas siempre tienen prioridad.

```bash
cp .env.example .env
```

## Línea de comandos

```
nodered-mcp                    arrancar el servidor (equivale a `nodered-mcp serve`)
nodered-mcp serve --read-only  arrancar sin las tools que modifican
nodered-mcp serve --help       listar todas las flags
nodered-mcp init               generar un snippet de configuración para tu cliente MCP
nodered-mcp update             detectar el canal de instalación y actualizar in situ
nodered-mcp version            imprimir la versión
```

### El comando init

`init` detecta qué clientes MCP hay instalados, pregunta la URL de Node-RED, el token y el directorio de backups, y **resuelve la ruta absoluta del binario en ejecución**. Así el snippet generado nunca apunta a un ejecutable inexistente, que es la causa habitual del error "Server disconnected".

| Invocación | Comportamiento |
|---|---|
| `nodered-mcp init` | Imprime el snippet para que lo pegues |
| `nodered-mcp init --write` | Lo escribe directamente en la configuración del cliente |
| `nodered-mcp init --all` | Muestra todos los clientes conocidos, no solo los detectados |

`--write` realiza un merge seguro que preserva cualquier otro servidor ya configurado y guarda un `.bak` del fichero anterior. Está soportado para Claude Desktop, Cursor y Gemini CLI. Para VS Code, cuya configuración es por workspace, y para Claude Code, que se configura mediante su propia CLI, `init` imprime la instrucción en su lugar.

### El comando update

`update` detecta cómo se instaló el binario y lo actualiza in situ. El canal del wrapper npm (la ruta de instalación recomendada) ejecuta `npm install -g @fgjcarlos/nodered-mcp@latest` tras un prompt de confirmación. Los canales Docker y binario standalone imprimen el comando de actualización para que el usuario lo ejecute.

| Invocación | Comportamiento |
|---|---|
| `nodered-mcp update` | Muestra la versión actual y la última, pide confirmación si hay una más reciente |
| `nodered-mcp update --yes` | Salta el prompt de confirmación |
| `nodered-mcp update --check` | Imprime la última versión y sale con 0 si es más reciente que la actual, 1 en caso contrario |

Orden de detección: `/.dockerenv` (Docker) → un `package.json` con nombre `@fgjcarlos/nodered-mcp` junto al binario, o un directorio por encima (npm) → binario standalone (install script). El canal npm lee la última versión del registro público; sin autenticación.

## Transportes

**stdio** (por defecto). El cliente MCP lanza el binario y se comunica por stdin/stdout. Es lo que usan Claude Desktop, Claude Code, Cursor, VS Code y Gemini CLI.

**http** (streamable HTTP). Un único proceso de larga duración atiende a varios clientes. El endpoint MCP está en `<addr>/mcp`.

```bash
nodered-mcp serve --transport http --http-addr :8090
```

### Autenticar el transporte HTTP

El transporte HTTP expone todas las tools — desplegar flows, instalar módulos, detener el runtime — a cualquiera que alcance el puerto. Por eso va protegido con un bearer token compartido.

```bash
nodered-mcp serve --transport http --http-addr :8090 --http-token "$(openssl rand -hex 32)"
```

Los clientes lo envían en una cabecera `Authorization` normal:

```
Authorization: Bearer <token>
```

**El token es obligatorio siempre que la dirección de escucha sea alcanzable desde fuera de la máquina, y el servidor se niega a arrancar sin él.** El caso que esto detecta es `:8090`: parece local y en realidad escucha en todas las interfaces. Un bind a loopback — `127.0.0.1:8090`, `localhost:8090`, `[::1]:8090` — no lo necesita, para no entorpecer el desarrollo local.

```bash
nodered-mcp serve --transport http --http-addr :8090
# nodered-mcp: loading config: MCP_HTTP_TOKEN is required: ":8090" is reachable
# from outside this machine ...
```

La comparación del token es de tiempo constante, así que un intento fallido no revela cuánto había acertado, y el token nunca aparece en una respuesta. Las peticiones rechazadas se registran con la dirección del cliente.

Esto no cifra el transporte: sobre una red no confiable, ponlo detrás de un proxy inverso que termine TLS. OAuth, que es lo que necesitan los clientes web alojados, no está implementado — el perfil de MCP exige un servidor de autorización completo, no una extensión de esto.

## Integración con clientes

Todos los ejemplos siguientes usan el transporte stdio. Para HTTP, consulta [la variante HTTP](#variante-http).

### Claude Desktop

**Recomendado: la extensión `.mcpb`.** Un instalador nativo que no requiere editar JSON. Genera el bundle con `scripts/build-mcpb.sh`, o descárgalo de Releases una vez publicado, y en Claude Desktop abre **Settings → Extensions → Install Extension** y selecciona el fichero `.mcpb`. Claude Desktop muestra un formulario para la URL de Node-RED, el token y el directorio de backups. El token se guarda en el almacén de credenciales del sistema operativo.

```bash
VERSION=v0.4.0 bash scripts/build-mcpb.sh   # requiere go y npx
```

**Alternativa manual.** Edita `claude_desktop_config.json` — `%APPDATA%\Claude\` en Windows, `~/Library/Application Support/Claude/` en macOS. Ver [`examples/claude_desktop_config.json`](./examples/claude_desktop_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-lo-tienes"
      }
    }
  }
}
```

### Claude Code

```bash
claude mcp add nodered \
  -e NODERED_URL=http://localhost:1880 \
  -e NODERED_TOKEN=tu-token \
  -- nodered-mcp
```

### Cursor

`.cursor/mcp.json` en tu workspace, o `~/.cursor/mcp.json` de forma global. Mismo formato que el snippet de Claude Desktop. Ver [`examples/cursor_mcp.json`](./examples/cursor_mcp.json).

### VS Code

`.vscode/mcp.json` en tu workspace. Ten en cuenta que la clave raíz es `servers`, no `mcpServers`. Ver [`examples/vscode_mcp.json`](./examples/vscode_mcp.json).

```json
{
  "servers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-lo-tienes"
      }
    }
  }
}
```

### Gemini CLI

`~/.gemini/settings.json`. Mismo formato que el snippet de Claude Desktop. Ver [`examples/gemini_settings.json`](./examples/gemini_settings.json).

### OpenCode

OpenCode usa una clave raíz `mcp` (no `mcpServers`) y declara cada servidor como `local` (comando lanzado) o `remote` (endpoint HTTP). Coloca el snippet en `~/.config/opencode/opencode.json` (global del usuario) o `./opencode.json` (local del proyecto). Ver [`examples/opencode_config.json`](./examples/opencode_config.json).

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "nodered": {
      "type": "local",
      "command": ["nodered-mcp"],
      "enabled": true,
      "environment": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-lo-tienes"
      }
    }
  }
}
```

Para la variante de transporte HTTP, usa `type: "remote"` con `url` y `headers`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "nodered": {
      "type": "remote",
      "url": "http://localhost:8090/mcp",
      "enabled": true,
      "headers": {
        "Authorization": "Bearer tu-token"
      }
    }
  }
}
```

Reinicia OpenCode tras editar. Las 29 tools deberían aparecer bajo el servidor `nodered`.

### Pi (pi-mono)

Pi expone MCP a través de un adaptador de terceros (`pi-mcp-adapter` / `pi-mcp-extension`), no en el núcleo. Instala ambos y después escribe la configuración:

```bash
npm install -g --ignore-scripts @earendil-works/pi-coding-agent
pi install mcp-adapter
```

Después escribe `~/.pi/agent/mcp.json` (global) o `./.pi/mcp.json` (local del proyecto). Ver [`examples/pi_mcp_config.json`](./examples/pi_mcp_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_TOKEN": "tu-token-si-lo-tienes"
      },
      "lifecycle": "keep-alive"
    }
  }
}
```

`lifecycle: "keep-alive"` es lo recomendado para nodered-mcp: reconecta automáticamente tras un reinicio de Node-RED, lo cual importa porque Node-RED se reinicia durante los despliegues de flows. El `lazy` por defecto solo conecta en la primera llamada a una tool y desconecta tras inactividad, lo que puede enmascarar problemas de conexión.

Dentro de Pi, ejecuta `/reload` para recargar la configuración, luego `mcp({ connect: "nodered" })` para verificar la conexión y `mcp({ server: "nodered" })` para listar las 29 tools.

Para la variante de transporte HTTP:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "auth": "bearer",
      "bearerToken": "tu-token"
    }
  }
}


### Variante HTTP

Arranca el servidor una vez y apunta el cliente al endpoint en lugar de a un comando. En clientes que soportan `url` o `type: http`:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "headers": { "Authorization": "Bearer tu-token" }
    }
  }
}
```

Omite el bloque `headers` solo si el servidor escucha en loopback y corre sin token.

Reinicia el cliente tras conectar. Deberían aparecer las 29 tools.

## Resolución de problemas

**No aparecen las tools.** Comprueba que el binario esté en el `PATH`, o usa una ruta absoluta en `command`. En Windows, escapa las barras invertidas: `C:\\ruta\\nodered-mcp.exe`. Ejecutar `nodered-mcp init` resuelve la ruta por ti.

**401 o 403 desde Node-RED.** Falta el token o carece del scope necesario. Con `adminAuth` activo, genera un token con permiso de escritura sobre flows.

**No hay salida de logs.** Los logs se escriben en stderr; stdout está reservado para las tramas JSON-RPC del transporte stdio. Aumenta el detalle con `--log-level debug`.

**El transporte HTTP no conecta.** Confirma que `--transport http` está activo y que el puerto de `--http-addr` está libre.

**Una escritura se rechaza con un error de backup.** Los backups fallan en cerrado por diseño: si el snapshot no se puede escribir, la escritura no se ejecuta. Comprueba que `NODERED_BACKUP_DIR` existe y tiene permisos de escritura.

## Arquitectura

```
cmd/nodered-mcp/     entrypoint, CLI, comando init
internal/config/     carga y validación de variables de entorno
internal/nodered/    cliente HTTP contra la admin API
internal/mcp/        servidor MCP: tools, resources, prompts
```

La capa MCP es deliberadamente delgada. Cada método del cliente se corresponde con exactamente un endpoint de la admin API; la capa MCP decide cómo exponer esas operaciones como tools.

Dependencias:

| Componente | Elección |
|---|---|
| Lenguaje | Go 1.25+ |
| SDK de MCP | [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — stdio y streamable HTTP |
| Cliente HTTP | `net/http` (biblioteca estándar) |
| WebSocket | [`coder/websocket`](https://github.com/coder/websocket) — el stream de debug de /comms. Sin dependencias propias |
| Logging | `log/slog` (biblioteca estándar) |
| Configuración de desarrollo | [`godotenv`](https://github.com/joho/godotenv) |

Sin frameworks, sin ORM, sin clientes de Node-RED de terceros.

## Desarrollo

```bash
go test ./...
go build -o nodered-mcp ./cmd/nodered-mcp
```

Verificación de extremo a extremo contra una instancia desechable:

```bash
docker run -it -p 1880:1880 nodered/node-red
go build -o nodered-mcp ./cmd/nodered-mcp
./nodered-mcp init --write
```

Después, pide a tu cliente que liste los flows de Node-RED.

Las tareas pendientes se registran en [`issues/`](./issues/README.md) hasta que el repositorio se traslade a GitHub Issues. Las decisiones de diseño están en [`PLAN.md`](./PLAN.md).

## Hoja de ruta

| Versión | Alcance | Estado |
|---|---|---|
| v0.1 | 10 tools, 1 resource, 2 prompts, transporte stdio | Publicada |
| v0.2 | Transporte streamable HTTP, CLI con flags y subcomandos | Publicada |
| v0.3 | Gestión de la palette: instalar, desinstalar, activar, desactivar | Publicada |
| v0.4 | `search_nodes`, ajustes y estado del runtime — 19 tools, 3 resources, 2 prompts | Publicada |
| v0.5 | Modo de solo lectura, lecturas eficientes en contexto, diagnóstico, contexto, stream de debug, edición granular de nodos, `diff_flows`, autenticación bearer HTTP — 29 tools | Publicada |
| v0.6 | Resource Server OAuth 2.1 para conectores web alojados | Publicada |
| v0.7 | Caché local de consultas | Prevista |

## Licencia

MIT. Ver [`LICENSE`](./LICENSE).

## Proyecto relacionado

[`nrcc`](https://github.com/fgjcarlos/nrcc) — Node-RED Control Center, un panel web en Go y React para administrar instancias de Node-RED. Es una base de código separada, sin código compartido, diseñada para convivir con este servidor.
