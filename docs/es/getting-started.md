# Primeros pasos

[English](../getting-started.md) | **Español**

Esta página es para ti si nunca has usado MCP ni Node-RED. Te lleva de cero a
tu primera conversación con tus propios flujos. Cualquier palabra que no
conozcas está explicada en el [glosario](glossary.md).

## Contenido

- [Qué es esto](#qué-es-esto)
- [Qué necesitas](#qué-necesitas)
- [Instalación](#instalación)
- [Conecta tu cliente](#conecta-tu-cliente)
- [Un token, si Node-RED pide inicio de sesión](#un-token-si-node-red-pide-inicio-de-sesión)
- [Tu primera conversación](#tu-primera-conversación)
- [Cómo mantenerte seguro](#cómo-mantenerte-seguro)
- [Próximos pasos](#próximos-pasos)

## Qué es esto

[Node-RED](glossary.md#node-red) es una herramienta donde se construyen
automatizaciones dibujando cajas y conectándolas con líneas; cada
automatización se llama [flujo](glossary.md#flujo-flow).
[MCP](glossary.md#mcp) es un enchufe común que permite a las apps de IA (el
[cliente](glossary.md#cliente)) usar otros programas. `nodered-mcp` es el
adaptador intermedio: piensa en él como un traductor en una recepción. Le
dices a la IA lo que quieres con palabras normales, el traductor lo convierte
en las solicitudes exactas que Node-RED entiende y te trae la respuesta. Así,
la IA puede leer, explicar y, si lo permites, modificar tus flujos.

## Qué necesitas

- Un **Node-RED en ejecución**. Por defecto, `nodered-mcp` lo busca en
  `http://localhost:1880`.
- Un **cliente MCP**: una app de IA que entienda MCP, como Claude Desktop,
  Claude Code o Cursor. [`clients.md`](clients.md) lista los compatibles.
- **Node.js 18 o superior**, solo si instalas con npm (la forma recomendada).

## Instalación

La forma recomendada es npm:

```bash
npm install -g @fgjcarlos/nodered-mcp
```

Otras formas de instalar:

- **Docker**: consulta la [sección Docker del README](../../README.es.md#docker).
- **`go install`**: consulta la [sección Instalación del README](../../README.es.md#instalación).

## Conecta tu cliente

`nodered-mcp init` te hace unas preguntas y luego genera la configuración que
necesita tu cliente. Ejecútalo en una terminal:

```bash
nodered-mcp init --write
```

Te pregunta:

1. **URL de Node-RED** (pulsa Enter para mantener `http://localhost:1880`).
2. **Token de Node-RED** (opcional; pulsa Enter para omitirlo, consulta
   [la siguiente sección](#un-token-si-node-red-pide-inicio-de-sesión)).
3. **Directorio de copias de seguridad** (pulsa Enter para mantener `backups`).

Si encuentra más de un cliente instalado, te pregunta cuál configurar. Si no
encuentra ninguno, se detiene; vuelve a ejecutarlo con `--all`.
Opciones útiles:

| Opción | Qué hace |
|---|---|
| `--write` | Guarda la configuración en el archivo de configuración del cliente. El archivo anterior se conserva como `.bak`. Funciona con Claude Desktop, Cursor y Gemini CLI. Para los demás clientes imprime lo que debes pegar o ejecutar e informa de que no se escribió nada. |
| `--dry-run` | Muestra exactamente lo que guardaría `--write`, sin modificar ningún archivo. No se puede combinar con `--write`. |
| `--all` | Lista todos los clientes conocidos, no solo los que detectó. |
| `--name <server>` | Usa otro nombre para la entrada (por defecto `nodered`). Útil con varias instancias de Node-RED. |

Sin `--write`, `init` solo imprime la configuración. Para Claude Code imprime
un comando `claude mcp add …` para que lo ejecutes. Los detalles de cada
cliente, incluida la configuración manual, están en
[`clients.md`](clients.md).

Cuando termine, **reinicia tu cliente**. Las [herramientas](glossary.md#herramienta-tool)
de Node-RED aparecerán entonces bajo el servidor llamado `nodered`.

## Un token, si Node-RED pide inicio de sesión

Omite esta sección si abres el editor de Node-RED sin iniciar sesión. Si tu
Node-RED tiene activada la autenticación de administración (`adminAuth`),
`nodered-mcp` necesita un [token](glossary.md#token) con permiso de escritura
sobre los flujos; de lo contrario, Node-RED responderá con un error 401 o 403.

Por seguridad, `init` nunca guarda tu token en un archivo de configuración.
Haz que esté disponible como la variable de entorno `NODERED_TOKEN` (o en el
llavero de tu sistema operativo o en un gestor de secretos) antes de que
arranque tu cliente. Trata el token como una contraseña. Más información en
[`configuration.md`](configuration.md) y [`troubleshooting.md`](troubleshooting.md).

## Tu primera conversación

Abre tu cliente y prueba estos tres mensajes. La redacción exacta no importa;
el lenguaje normal funciona.

1. **«Lista mis flujos.»** La IA llama a la herramienta `list_flows` y responde
   con las [pestañas](glossary.md#pestaña-tab) de tu Node-RED. Esto solo lee;
   no cambia nada.
2. **«Explica qué hace el flujo llamado Calefacción.»** La IA lee ese flujo
   (`get_flow`; el servidor también ofrece un [prompt](glossary.md#prompt)
   listo para usar, `explain_flow`) y describe sus disparadores y con qué se
   comunica. De nuevo, solo lectura.
3. **«Crea un flujo que registre un mensaje cada 5 minutos.»** La IA construye
   el flujo (el servidor ofrece un prompt `generate_flow` para esto) y lo
   añade con `create_flow`. Esto *modifica* Node-RED, así que antes se guarda
   una [copia de seguridad](glossary.md#copia-de-seguridad-backup) de tus
   flujos. Muchos clientes también te piden aprobar cada llamada a una
   herramienta; léela antes de aceptar.

Después abre el editor de Node-RED para ver el resultado.

## Cómo mantenerte seguro

Dar a una IA el poder de editar un sistema de automatización merece cuidado.
Esto viene incorporado:

- **Modo solo lectura.** Inicia el servidor con `--read-only`, o con la
  variable de entorno `MCP_READ_ONLY=true`, y solo se ofrecen las 21
  herramientas de lectura; se ocultan todas las que cambian algo, incluida
  `inject_node`. Es un buen ajuste inicial mientras aprendes. Consulta
  [`configuration.md`](configuration.md).
- **Copias de seguridad automáticas.** Antes de una escritura que cambie la
  configuración de los flujos, se guarda una instantánea (en `backups` por
  defecto, se conservan 50). Si la instantánea no se puede guardar, la
  escritura no se realiza. Las instantáneas no incluyen las credenciales de
  Node-RED, así que conserva también tu propia copia; consulta los
  [límites de recuperación de copias de seguridad](tools.md#límites-de-la-recuperación-de-copias-de-seguridad).
- **Lista de bloqueo.** Los nodos de los tipos `exec` y `system`, que ejecutan
  comandos en tu computadora, se rechazan por defecto (`MCP_NODE_DENYLIST`).
  Consulta [`SECURITY.md`](../../SECURITY.md) antes de cambiarlo.

## Próximos pasos

- Recorre el [glosario](glossary.md) para cualquier palabra que no conocieras.
- Consulta todas las herramientas que puede usar la IA en [`tools.md`](tools.md).
- Ajusta el servidor en [`configuration.md`](configuration.md).
- ¿Algo no funciona? [`troubleshooting.md`](troubleshooting.md).
- Para ejecutarlo para varias personas o a través de la red:
  [`transports.md`](transports.md).
- Toda la documentación: [`README.md`](README.md).
