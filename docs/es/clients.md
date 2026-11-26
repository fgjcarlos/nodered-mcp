# Integración con clientes MCP

[English](../clients.md) | **Español**

Esta página es para conectar una app de IA (un [cliente](glossary.md#cliente)
MCP, como Claude Desktop, Claude Code o Cursor) a Node-RED mediante
`nodered-mcp`. Cada cliente mantiene un pequeño archivo de configuración que
lista los servidores MCP que puede iniciar; tú añades una entrada que dice
«ejecuta `nodered-mcp` y habla con este Node-RED». La necesitas justo después
de instalar, o cuando añadas otra app u otra instancia de Node-RED. ¿Eres
nuevo en todo esto? Lee primero los [primeros pasos](getting-started.md).

**Contenido**

- [Varias instancias de Node-RED](#varias-instancias-de-node-red)
- [Vista previa de una fusión](#vista-previa-de-una-fusión)
- [Claude Desktop](#claude-desktop)
- [Claude Code](#claude-code)
- [Cursor](#cursor)
- [VS Code](#vs-code)
- [Gemini CLI](#gemini-cli)
- [OpenCode](#opencode)
- [Pi](#pi)
- [Variante HTTP](#variante-http)

**Cómo usar esta página**

1. El camino más fácil: ejecuta `nodered-mcp init`. Te hace unas preguntas
   (qué cliente, la URL de Node-RED) e imprime el snippet para tu cliente. Con
   `--write` también edita por ti el archivo de configuración del cliente,
   cuando ese cliente tiene un archivo compatible.
2. O busca tu cliente en la lista de abajo y copia su snippet a mano.
3. Reinicia el cliente para que lea la nueva configuración.

Todos los ejemplos de abajo usan el transporte [stdio](glossary.md#stdio): el
cliente inicia `nodered-mcp` por sí mismo. Para [HTTP](glossary.md#streamable-http),
consulta la sección «Variante HTTP» al final.

`nodered-mcp init` nunca escribe `NODERED_TOKEN` en los snippets generados ni
en los archivos de configuración. El [token](glossary.md#token) es el valor,
parecido a una contraseña, que Node-RED te da para demostrar quién eres.
Defínelo en el entorno del proceso del cliente, en el llavero del sistema
operativo o en un gestor de secretos antes de iniciar el cliente.
`init --write` solo funciona con los clientes que tienen un destino de
configuración compatible; para los demás imprime instrucciones manuales y
termina con un error, para que la automatización pueda detectar que no se
aplicó nada.

Usa `nodered-mcp init --name nodered-lab` para registrar una segunda instancia
de Node-RED con otro nombre de servidor. Los nombres admiten letras ASCII,
dígitos, guiones bajos y guiones, pero no pueden empezar con un guion. Cuando
se te pida, introduce la URL de la instancia y un directorio de copias de
seguridad absoluto y distinto.

Tanto `--write` como `--dry-run` conservan las demás entradas de servidor. Si
un nombre pasado con `--name` ya existe, `--write` pide confirmación y
`--dry-run` muestra la fusión prevista sin preguntar. Si omites `--name`, se
mantiene el comportamiento anterior: `--write` actualiza la entrada `nodered`
sin preguntar.

## Varias instancias de Node-RED

En la práctica: si tienes un Node-RED de pruebas en el puerto `1880` y uno de
producción en el `1881`, registras dos servidores (por ejemplo `nodered-1880` y
`nodered-1881`) para poder indicarle al asistente en cuál trabajar.

Ejecuta un servidor `nodered-mcp` por cada instancia de Node-RED. Dale a cada
servidor un nombre único, su propia `NODERED_URL` y sus credenciales, y un
`NODERED_BACKUP_DIR` **absoluto** y separado, en el que el servidor pueda
escribir.

> [!WARNING]
> No compartas un directorio de copias de seguridad. El valor por defecto,
> `backups`, es relativo al directorio de trabajo del proceso MCP. Si dos
> servidores resuelven al mismo directorio, `restore_backup` con `latest`
> puede desplegar los flujos de otra instancia, y `diff_flows` con `latest`
> puede comparar contra la instantánea equivocada.

Los ejemplos de abajo se conectan a Node-RED en los puertos `1880` y `1881`.
Sustituye `/home/you` por tu directorio personal real. Inicia Claude Code con
`NODERED_TOKEN_1880` y `NODERED_TOKEN_1881` definidos en su entorno con los
bearer tokens de las respectivas instancias. Si una instancia no requiere
autenticación, omite su ajuste `NODERED_TOKEN`. Para otras opciones de
autenticación, consulta [Conexión con Node-RED](configuration.md#conexión-con-node-red).

**JSON (`.mcp.json` de Claude Code):** Añade estas entradas dentro de
`mcpServers`. Claude Code expande las referencias a los tokens al iniciar; no
pongas los valores de los tokens en este archivo.

```json
{
  "mcpServers": {
    "nodered-1880": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880",
        "NODERED_BACKUP_DIR": "/home/you/.local/share/nodered-mcp/1880/backups",
        "NODERED_TOKEN": "${NODERED_TOKEN_1880}"
      }
    },
    "nodered-1881": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1881",
        "NODERED_BACKUP_DIR": "/home/you/.local/share/nodered-mcp/1881/backups",
        "NODERED_TOKEN": "${NODERED_TOKEN_1881}"
      }
    }
  }
}
```

**CLI de Claude Code:** Como alternativa, ejecuta estos comandos desde el
directorio del proyecto. Mantén las comillas simples para que el shell pase
las referencias a los tokens literalmente, en lugar de poner los valores de
los tokens en la configuración guardada.

```bash
claude mcp add --scope project \
  --env NODERED_URL=http://localhost:1880 \
  --env NODERED_BACKUP_DIR=/home/you/.local/share/nodered-mcp/1880/backups \
  --env 'NODERED_TOKEN=${NODERED_TOKEN_1880}' \
  --transport stdio nodered-1880 -- nodered-mcp

claude mcp add --scope project \
  --env NODERED_URL=http://localhost:1881 \
  --env NODERED_BACKUP_DIR=/home/you/.local/share/nodered-mcp/1881/backups \
  --env 'NODERED_TOKEN=${NODERED_TOKEN_1881}' \
  --transport stdio nodered-1881 -- nodered-mcp
```

Ejecuta `claude mcp list` para comprobar que ambos nombres están registrados.
Antes de restaurar una instantánea, asegúrate de haber seleccionado el
servidor MCP correcto.

## Vista previa de una fusión

En la práctica: ejecuta `nodered-mcp init --dry-run` para ver qué se
escribiría antes de dejar que `--write` toque tu archivo de configuración.

`nodered-mcp init --dry-run` imprime la configuración que `--write`
fusionaría, sin tocar el sistema de archivos. No crea ningún archivo, ningún
directorio padre, ninguna copia `.bak` ni ningún archivo temporal, y no cambia
ningún permiso. Es útil cuando tu configuración ya contiene otros servidores
MCP y quieres verificar el resultado real de la fusión antes de aplicarla: el
snippet que imprime un `init` normal es un ejemplo recién generado, no la
fusión.

La vista previa coincide con el archivo que produciría `--write`, con una
excepción deliberada: donde la vista previa muestra `REDACTED`, el archivo
conserva el valor real. Todo lo demás —claves, estructura, la entrada
seleccionada, tus otros servidores— es idéntico.

Los secretos se ocultan en la vista previa: la entrada de servidor generada
nunca lleva `NODERED_TOKEN`, y los valores con secretos que pertenecen a otros
servidores ya presentes en tu archivo también se enmascaran. La ocultación
compara nombres de clave (`NODERED_TOKEN`, `TOKEN`, `API_KEY`, `PASSWORD`,
`SECRET`) de forma exacta, así que un secreto guardado bajo una clave
inesperada, dentro de un arreglo `args`, incrustado en una URL o como un valor
que no es una cadena **no** se enmascara. Trata la salida de la vista previa
como sensible si tu configuración guarda tokens de forma poco habitual.

Se aplica la misma limitación que con `--write`: para los clientes sin un
destino de configuración seguro (Claude Code, VS Code, OpenCode, Pi), la vista
previa imprime el mismo snippet manual que imprime un `init` normal con tus
respuestas (también sin `NODERED_TOKEN`) y termina con un error, para que la
automatización pueda detectar que no se fusionó nada. `--dry-run` y `--write`
juntos se rechazan por contradictorios.

## Claude Desktop

Edita `claude_desktop_config.json` — `%APPDATA%\Claude\` en Windows,
`~/Library/Application Support/Claude/` en macOS. Consulta
[`examples/claude_desktop_config.json`](../../examples/claude_desktop_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880"
      }
    }
  }
}
```

## Claude Code

```bash
claude mcp add -s user nodered \
  -e NODERED_URL=http://localhost:1880 \
  -- nodered-mcp
```

`-s user` registra el servidor para toda tu cuenta. Sin él,
`claude mcp add` usa por defecto `--scope local`, que vincula el servidor al
directorio en el que ejecutaste el comando: entonces no aparece en ningún otro
proyecto, sin ningún error que explique el motivo. Comprueba el resultado con
`claude mcp list`.

## Cursor

`.cursor/mcp.json` en tu espacio de trabajo, o `~/.cursor/mcp.json` de forma
global. Misma estructura que el snippet de Claude Desktop de arriba. Consulta
[`examples/cursor_mcp.json`](../../examples/cursor_mcp.json).

## VS Code

`.vscode/mcp.json` en tu espacio de trabajo. Ten en cuenta que la clave raíz
es `servers`, no `mcpServers`. Consulta
[`examples/vscode_mcp.json`](../../examples/vscode_mcp.json).

```json
{
  "servers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880"
      }
    }
  }
}
```

## Gemini CLI

`~/.gemini/settings.json`. Misma estructura que el snippet de Claude Desktop.
Consulta [`examples/gemini_settings.json`](../../examples/gemini_settings.json).

## OpenCode

OpenCode usa una clave `mcp` de nivel superior (no `mcpServers`) y declara
cada servidor como `local` (comando lanzado) o `remote` (endpoint HTTP).
Coloca el snippet en `~/.config/opencode/opencode.json` (global del usuario) o
en `./opencode.json` (local del proyecto). Consulta
[`examples/opencode_config.json`](../../examples/opencode_config.json).

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "nodered": {
      "type": "local",
      "command": ["nodered-mcp"],
      "enabled": true,
      "environment": {
        "NODERED_URL": "http://localhost:1880"
      }
    }
  }
}
```

Para la variante con transporte HTTP, usa `type: "remote"` con `url` y
`headers`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "nodered": {
      "type": "remote",
      "url": "http://localhost:8090/mcp",
      "enabled": true,
      "headers": {
        "Authorization": "Bearer your-token"
      }
    }
  }
}
```

Reinicia OpenCode después de editar. Las 44 herramientas deberían aparecer
bajo el servidor `nodered`.

## Pi

Pi (pi-mono) incluye soporte MCP mediante un adaptador de terceros
(`pi-mcp-adapter` / `pi-mcp-extension`), no en el núcleo. Instala ambos y
luego escribe la configuración:

```bash
npm install -g --ignore-scripts @earendil-works/pi-coding-agent
pi install mcp-adapter
```

Después escribe `~/.pi/agent/mcp.json` (global) o `./.pi/mcp.json` (local del
proyecto). Cuando `PI_CODING_AGENT_DIR` está definida, Pi lee el archivo
global desde `$PI_CODING_AGENT_DIR/mcp.json` en su lugar, y
`nodered-mcp init` indica esa ruta. Consulta
[`examples/pi_mcp_config.json`](../../examples/pi_mcp_config.json).

```json
{
  "mcpServers": {
    "nodered": {
      "command": "nodered-mcp",
      "env": {
        "NODERED_URL": "http://localhost:1880"
      },
      "lifecycle": "keep-alive"
    }
  }
}
```

`lifecycle: "keep-alive"` se recomienda para nodered-mcp: se reconecta
automáticamente tras un reinicio de Node-RED, lo que importa porque Node-RED
se reinicia durante los despliegues de flujos. El valor por defecto, `lazy`,
solo se conecta en la primera llamada a una herramienta y se desconecta tras
un periodo de inactividad, lo que puede ocultar problemas de conexión.

Dentro de Pi, ejecuta `/reload` para cargar la configuración, luego
`mcp({ connect: "nodered" })` para verificar la conexión y
`mcp({ server: "nodered" })` para listar las 44 herramientas.

Para la variante con transporte HTTP:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "auth": "bearer",
      "bearerToken": "your-token"
    }
  }
}
```

## Variante HTTP

Úsala cuando un proceso `nodered-mcp` se ejecuta por su cuenta (consulta
[Transportes](transports.md)) y el cliente se conecta a él por la red en lugar
de iniciarlo. Inicia el servidor una vez y luego apunta el cliente al endpoint
en lugar de a un comando. En los clientes que admiten `url` o `type: http`:

```json
{
  "mcpServers": {
    "nodered": {
      "url": "http://localhost:8090/mcp",
      "headers": { "Authorization": "Bearer your-token" }
    }
  }
}
```

Sustituye `your-token` por el valor de `MCP_HTTP_TOKEN` definido en el
servidor. Elimina el bloque `headers` solo si el servidor está enlazado a
loopback (accesible únicamente desde esta computadora) y se ejecuta sin token.

Reinicia el cliente tras conectarlo. Las 44 herramientas deberían aparecer.
