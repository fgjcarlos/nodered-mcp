# Configuración

[English](../configuration.md) | **Español**

Esta página lista todos los ajustes que entiende `nodered-mcp`: dónde está tu
Node-RED, adónde van las copias de seguridad, si el asistente puede cambiar
algo y cómo se protege el modo de red. La necesitas cuando los valores por
defecto no encajan (Node-RED no está en `localhost:1880`, pide una
contraseña, quieres una configuración más segura de solo lectura) o cuando
quieres consultar qué hace una variable. La mayoría de las personas solo toca
`NODERED_URL` y `NODERED_TOKEN`.

## Contenido

- [Cómo funcionan los ajustes](#cómo-funcionan-los-ajustes)
- [Conexión con Node-RED](#conexión-con-node-red)
- [Copias de seguridad](#copias-de-seguridad)
- [Comportamiento del servidor](#comportamiento-del-servidor)
- [Transporte HTTP](#transporte-http)
- [Seguridad](#seguridad)

## Cómo funcionan los ajustes

22 variables de entorno. Once de ellas también aceptan un flag de línea de
comandos; el resto son solo de entorno. Precedencia: **flag > env > valor por
defecto.**

Una *variable de entorno* es un ajuste con nombre que se pasa a un programa
desde fuera, por ejemplo en el bloque `env` de la configuración de tu
[cliente](glossary.md#cliente) MCP (consulta la
[integración con clientes](clients.md)). Un *flag* es el mismo ajuste escrito
después del comando, como `--url`. Si defines ambos, gana el flag; si no
defines ninguno, se aplica el valor por defecto de las tablas de abajo.

En la práctica, estas dos formas son equivalentes:

```bash
NODERED_URL=http://192.168.1.20:1880 nodered-mcp
nodered-mcp --url http://192.168.1.20:1880
```

Para que el asistente solo lea y nunca cambie nada, define
`MCP_READ_ONLY=true` (o pasa `--read-only`); consulta el
[modo solo lectura](glossary.md#modo-solo-lectura).

## Conexión con Node-RED

Indica al servidor dónde está Node-RED y cómo iniciar sesión. Si Node-RED no
tiene el inicio de sesión activado, los valores por defecto funcionan tal
cual. Si usa la [API de administración](glossary.md#api-de-administración) con
autenticación, proporciona un token (o, como alternativa, un usuario y una
contraseña).

| Variable | Valor por defecto | Flag | Descripción |
|---|---|---|---|
| `NODERED_URL` | `http://localhost:1880` | `--url` | URL base de Node-RED |
| `NODERED_TOKEN` | *(vacío)* | `--token` | [Token](glossary.md#token) bearer |
| `NODERED_USERNAME` | *(vacío)* | — | Autenticación básica (alternativa) |
| `NODERED_PASSWORD` | *(vacío)* | — | Autenticación básica (alternativa) |
| `NODERED_INSECURE` | `false` | — | Omitir la verificación TLS |

### Tokens en la URL

`NODERED_URL` puede llevar un token en su cadena de consulta
(`http://node-red:1880?token=…`). El MCP nunca repite la cadena de consulta ni
la información de usuario de esa URL: una línea de registro nombra la URL
ocultada, y un error HTTP nombra solo la ruta de la solicitud —

```
nodered GET /flow/global: HTTP 404: Cannot GET /flow/global
```

Un error de configuración (una URL base que no se puede interpretar, una URL
que no se pudo unir, una solicitud que no se pudo construir) nombra el motivo
del fallo, no la URL en la que falló.

El **cuerpo** de un error es el que devolvió Node-RED y no se oculta: un
runtime que repita un secreto en el cuerpo de su respuesta lo incluye en el
error.

## Copias de seguridad

| Variable | Valor por defecto | Flag | Descripción |
|---|---|---|---|
| `NODERED_BACKUP_DIR` | `backups` | — | Dónde se escriben las instantáneas. Las rutas relativas se resuelven contra el directorio de trabajo del proceso MCP. |
| `NODERED_BACKUP_KEEP` | `50` | — | Cuántos archivos de copia de seguridad se conservan. Los más antiguos se eliminan tras cada nueva instantánea; `0` desactiva por completo la eliminación |

Al conectarte a [varias instancias de Node-RED](clients.md#varias-instancias-de-node-red),
define un directorio de copias de seguridad absoluto distinto para cada
servidor MCP. Los servidores que comparten un directorio pueden recoger las
instantáneas de los demás.

En la imagen de contenedor el directorio de copias de seguridad es
`/var/lib/nodered-mcp/backups` (la imagen define `NODERED_BACKUP_DIR` con esa
ruta y se ejecuta como el usuario `nonroot`, así que la ruta es escribible).
La imagen incluye ese directorio con la propiedad del usuario `nonroot`, de
modo que un volumen montado sobre él hereda esa propiedad. Monta un volumen
distinto por contenedor en esa ruta para conservar las instantáneas cuando se
recrea un contenedor, sin compartir copias entre instancias:

```bash
docker run --rm --name nodered-mcp \
  --volume nodered-backups:/var/lib/nodered-mcp/backups \
  ...
```

## Comportamiento del servidor

Nivel de detalle del registro, qué [transporte](glossary.md#transporte) usar y
[modo solo lectura](glossary.md#modo-solo-lectura).

| Variable | Valor por defecto | Flag | Descripción |
|---|---|---|---|
| `MCP_LOG_LEVEL` | `info` | `--log-level` | debug / info / warn / error |
| `MCP_TRANSPORT` | `stdio` | `--transport` | `stdio` o `http` |
| `MCP_READ_ONLY` | `false` | `--read-only` | Registra solo las 21 herramientas de lectura, ninguna de escritura |
| `MCP_DEBUG_STREAM` | `false` | `--debug-stream` | Abre el WebSocket `/comms` al arrancar para habilitar el flujo de depuración. **Desactivado por defecto** porque algunas versiones de Node-RED fallan durante el handshake (#17). Tras activarlo, `get_debug_messages` tarda ~3 s en empezar a recibir mensajes |
| `MCP_LIST_FLOWS_FULL_THRESHOLD` | `200` | — | Número de nodos a partir del cual `list_flows` con `detail="full"` se niega a volcar toda la configuración, salvo que quien llama pase `force=true`. Protege la ventana de contexto del modelo en despliegues grandes |

## Transporte HTTP

Solo se consulta cuando `MCP_TRANSPORT=http` (consulta los
[transportes](transports.md) y [streamable HTTP](glossary.md#streamable-http)).
Omite esta sección si tu cliente inicia `nodered-mcp` por sí mismo mediante
[stdio](glossary.md#stdio).

| Variable | Valor por defecto | Flag | Descripción |
|---|---|---|---|
| `MCP_HTTP_ADDR` | `:8090` | `--http-addr` | Dirección de escucha del transporte http |
| `MCP_HTTP_TOKEN` | *(vacío)* | `--http-token` | Bearer token del transporte http. **Obligatorio** si la dirección no es loopback |
| `MCP_HTTP_MAX_BODY` | `33554432` (32 MiB) | — | Límite del cuerpo de una única solicitud, en bytes. Se aplica antes de ejecutar cualquier manejador, de modo que un POST demasiado grande no pueda agotar la memoria |
| `MCP_HTTP_RATE_PER_SEC` | `1.0` | — | Solicitudes por segundo en régimen estable permitidas por IP de origen |
| `MCP_HTTP_RATE_BURST` | `10` | — | Cuántas solicitudes puede enviar de golpe una misma IP de origen antes de que se limite |
| `MCP_HTTP_RATE_DISABLED` | `false` | — | Desactiva por completo el limitador de solicitudes. Para pruebas, entornos aislados o cuando la limitación se gestiona en otra capa |
| `MCP_ALLOW_INSECURE_LOOPBACK` | `false` | `--allow-insecure-loopback` | Silencia la advertencia de arranque sobre un enlace a loopback sin autenticación. **No añade autenticación**: solo reconoce el compromiso |
| `MCP_OAUTH_ISSUER` | *(vacío)* | `--oauth-issuer` | Habilita OAuth 2.1 / OIDC en el transporte HTTP. Mutuamente excluyente con `MCP_HTTP_TOKEN` |
| `MCP_OAUTH_AUDIENCE` | *(vacío)* | `--oauth-aud` | Claim de audiencia requerida cuando hay un emisor definido |

> **«Por IP de origen» significa la dirección del socket.** El limitador
> resuelve el cliente a partir de `RemoteAddr` y no lee ninguna cabecera de la
> solicitud. Detrás de un proxy inverso, por tanto, todos los clientes
> comparten el cupo del proxy: un cliente ruidoso puede limitar a todos los
> demás. Aplica los límites por cliente en el proxy, donde se conoce la
> dirección real del cliente, y mantén el requisito de token MCP u OAuth.
> `X-Forwarded-For` se ignora deliberadamente: confiar en ella sin una lista
> explícita de proxies de confianza permite la suplantación. Consulta
> [Rate limiting behind a reverse proxy](../../SECURITY.md#rate-limiting-behind-a-reverse-proxy).

## Seguridad

La [lista de bloqueo](glossary.md#lista-de-bloqueo-denylist) nombra los tipos
de nodo que el asistente nunca puede añadir a tus flujos.

| Variable | Valor por defecto | Flag | Descripción |
|---|---|---|---|
| `MCP_NODE_DENYLIST` | `exec,system` | — | Tipos de nodo que rechaza toda escritura capaz de desplegar un tipo de nodo, desde `add_node` hasta `import_flow`, `set_flows` y `restore_backup`. Estos dos ejecutan comandos de shell como el proceso de Node-RED, así que permitirlos convierte las herramientas de escritura en ejecución remota de código. Define la cadena vacía para desactivarlo — consulta [`SECURITY.md`](../../SECURITY.md) |

`--http-addr :8090` **no es** loopback: escucha en todas las interfaces. Por
eso el token es obligatorio ahí.

`nodered-mcp version` imprime la versión. Si se instaló con
`go install …@latest`, la versión visible es la etiqueta de git resuelta,
porque `resolveVersion` (`cmd/nodered-mcp/main.go`) la recupera de la
información de compilación de la toolchain.

Para el desarrollo local, un archivo `.env` en el directorio de trabajo se
carga automáticamente. Las variables de entorno ya definidas siempre
prevalecen.

```bash
cp .env.example .env
```
