# Glosario

[English](../glossary.md) | **Español**

Significados en lenguaje sencillo de las palabras usadas en esta
documentación, en orden alfabético. ¿Eres nuevo? Lee primero los
[primeros pasos](getting-started.md).

## Contenido

[API de administración](#api-de-administración) · [Cliente](#cliente) ·
[Contexto](#contexto) · [Copia de seguridad](#copia-de-seguridad-backup) ·
[Flujo](#flujo-flow) · [Herramienta](#herramienta-tool) ·
[Lista de bloqueo](#lista-de-bloqueo-denylist) · [MCP](#mcp) ·
[Modo solo lectura](#modo-solo-lectura) · [Node-RED](#node-red) ·
[Nodo](#nodo) · [Paleta](#paleta) · [Pestaña](#pestaña-tab) ·
[Prompt](#prompt) · [Recurso](#recurso) · [Servidor](#servidor) ·
[stdio](#stdio) · [Streamable HTTP](#streamable-http) ·
[Subflujo](#subflujo-subflow) · [Token](#token) ·
[Transporte](#transporte)

## Términos

### API de administración

La interfaz web que [Node-RED](#node-red) ofrece para gestionarse a sí mismo:
leer y desplegar [flujos](#flujo-flow), instalar nodos, leer la
configuración. `nodered-mcp` habla con Node-RED únicamente a través de ella.
Más a fondo: [arquitectura](architecture.md).

### Cliente

La app de IA con la que hablas, como Claude Desktop, Claude Code o Cursor. Se
conecta a un [servidor](#servidor) y usa sus [herramientas](#herramienta-tool).
Más a fondo: [clientes](clients.md).

### Contexto

Memoria que Node-RED guarda para valores compartidos entre [nodos](#nodo), a
nivel de nodo, de [flujo](#flujo-flow) o global. Las herramientas
`get_context` y `set_context` la leen y la escriben. Más a fondo:
[herramientas](tools.md#runtime-diagnóstico-y-recuperación).

### Copia de seguridad (backup)

Una copia guardada de la configuración de tus flujos. `nodered-mcp` hace una
automáticamente antes de una escritura que cambie los flujos, y puedes
listarlas y restaurarlas con herramientas. Una copia de seguridad no incluye
las credenciales guardadas de Node-RED. Más a fondo:
[límites de recuperación de copias de seguridad](tools.md#límites-de-la-recuperación-de-copias-de-seguridad).

### Flujo (flow)

Una automatización en [Node-RED](#node-red): [nodos](#nodo) conectados por
líneas (cables), dibujados en una [pestaña](#pestaña-tab). Varios documentos
usan «flujo» para referirse a la propia pestaña, igual que la API de
Node-RED. Más a fondo: [herramientas](tools.md#flujos).

### Herramienta (tool)

Una acción que la IA puede pedir a un [servidor](#servidor), como
`list_flows` o `create_flow`. Cada una se clasifica como de lectura,
escritura o acción. Más a fondo: [herramientas](tools.md).

### Lista de bloqueo (denylist)

Una lista de tipos de nodo que `nodered-mcp` se niega a añadir o desplegar.
Por defecto bloquea `exec` y `system`, que ejecutan comandos en tu
ordenador. Se configura con `MCP_NODE_DENYLIST`. Más a fondo:
[configuración](configuration.md#seguridad) y
[`SECURITY.md`](../../SECURITY.md).

### MCP

Model Context Protocol, una forma común para que las apps de IA usen
programas externos. Un [cliente](#cliente) MCP se conecta a un
[servidor](#servidor) MCP, que ofrece [herramientas](#herramienta-tool),
[recursos](#recurso) y [prompts](#prompt). Sitio oficial:
[modelcontextprotocol.io](https://modelcontextprotocol.io). Más a fondo:
[arquitectura](architecture.md).

### Modo solo lectura

Un ajuste (`--read-only` o `MCP_READ_ONLY=true`) con el que el servidor ofrece
solo las herramientas que leen, de modo que la IA no puede cambiar nada. Más a
fondo: [configuración](configuration.md) y [herramientas](tools.md).

### Node-RED

Una herramienta gratuita para construir automatizaciones conectando
[nodos](#nodo) en un editor dentro del navegador, usada a menudo en domótica,
IoT y procesamiento de datos. `nodered-mcp` permite que una app de IA trabaje
con ella. Sitio oficial: [nodered.org](https://nodered.org).

### Nodo

Una caja de un [flujo](#flujo-flow) que hace una tarea pequeña: recibir un
mensaje, modificarlo, enviarlo a otro lugar. Cada nodo tiene un tipo, como
`inject`, `function` o `exec`. Más a fondo: [herramientas](tools.md#flujos).

### Paleta

La colección de tipos de nodo instalados en Node-RED, que se muestra en el
lado izquierdo de su editor. Las herramientas pueden listarlos, buscarlos,
instalarlos, activarlos y desactivarlos. Más a fondo:
[herramientas](tools.md#paleta).

### Pestaña (tab)

Una página del editor de Node-RED, que contiene un [flujo](#flujo-flow). Las
herramientas que trabajan sobre un flujo completo reciben el identificador de
la pestaña. Más a fondo: [herramientas](tools.md#flujos).

### Prompt

Dos significados. En el uso cotidiano, el mensaje que le escribes a la IA. En
[MCP](#mcp), una plantilla de instrucciones lista para usar que ofrece un
[servidor](#servidor); este ofrece `explain_flow` y `generate_flow`. Más a
fondo: [herramientas](tools.md#prompts-2).

### Recurso

Un dato de solo lectura que un [servidor](#servidor) [MCP](#mcp) ofrece bajo
una dirección, por ejemplo los flujos actuales en `nodered://flows/current`.
Más a fondo: [herramientas](tools.md#recursos-3).

### Servidor

Un programa que ofrece [herramientas](#herramienta-tool) a un
[cliente](#cliente). `nodered-mcp` es un servidor MCP. No hay que confundirlo
con la máquina que ejecuta Node-RED. Más a fondo:
[arquitectura](architecture.md).

### stdio

El [transporte](#transporte) por defecto: el cliente inicia `nodered-mcp` por
sí mismo y se intercambian mensajes a través de la entrada y la salida
estándar del programa. No se abre ningún puerto de red. Más a fondo:
[transportes](transports.md#stdio-predeterminado).

### Streamable HTTP

Un [transporte](#transporte) en el que `nodered-mcp` se ejecuta como un
servidor web de larga duración (endpoint `/mcp`) al que se conectan uno o más
clientes. Necesita un [token](#token), salvo que escuche solo en la máquina
local. Más a fondo:
[transportes](transports.md#http-streamable-http).

### Subflujo (subflow)

Un grupo de [nodos](#nodo) guardado como un único nodo reutilizable, para
poder usar la misma lógica en varios [flujos](#flujo-flow). Más a fondo:
[herramientas](tools.md#flujos).

### Token

Una cadena secreta larga que se usa como una contraseña. `NODERED_TOKEN`
permite que `nodered-mcp` inicie sesión en un Node-RED que lo exige;
`MCP_HTTP_TOKEN` protege el [transporte](#transporte) HTTP. No lo compartas
nunca. Más a fondo: [configuración](configuration.md).

### Transporte

La forma en que un [cliente](#cliente) y un [servidor](#servidor) intercambian
mensajes: [stdio](#stdio) o [streamable HTTP](#streamable-http). Más a fondo:
[transportes](transports.md).
