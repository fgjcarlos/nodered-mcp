# Transportes

[English](../transports.md) | **Español**

Un [transporte](glossary.md#transporte) es el canal que usan la app de IA y
`nodered-mcp` para comunicarse. Esta página explica los dos canales
disponibles, cuál conviene elegir y cómo proteger el que funciona por red.
Solo la necesitas si quieres compartir un servidor entre varias apps o
ordenadores, o si te da curiosidad cómo funciona la conexión; en caso
contrario, el valor por defecto es suficiente.

## Contenido

- [¿Qué transporte necesito?](#qué-transporte-necesito)
- [stdio (predeterminado)](#stdio-predeterminado)
- [http (streamable HTTP)](#http-streamable-http)
  - [Autenticación del transporte HTTP](#autenticación-del-transporte-http)
- [OAuth 2.1 (alternativa al bearer token)](#oauth-21-alternativa-al-bearer-token)

## ¿Qué transporte necesito?

| Si tú... | Usa |
|---|---|
| ejecutas una app de IA en el mismo ordenador que `nodered-mcp` | stdio (predeterminado, no hay nada que configurar) |
| quieres un servidor de larga duración compartido por varias apps o máquinas | http |

En la práctica: con stdio la configuración de tu cliente dice
`"command": "nodered-mcp"` y la app inicia el servidor por ti; con http
inicias el servidor tú mismo y la configuración del cliente dice
`"url": "http://localhost:8090/mcp"`. Consulta la
[integración con clientes](clients.md) para ver ambas formas. El ajuste es
`MCP_TRANSPORT` / `--transport` (consulta la [configuración](configuration.md)).

## stdio (predeterminado)

El [cliente](glossary.md#cliente) MCP lanza el binario y se comunica por
stdin / stdout (la entrada y salida de texto simple del programa, sin red de
por medio; consulta [stdio](glossary.md#stdio)). Es lo que usan Claude
Desktop, Claude Code, Cursor, VS Code y Gemini CLI.

## http (streamable HTTP)

Un único proceso de larga duración atiende a varios clientes
([streamable HTTP](glossary.md#streamable-http) es el nombre que MCP da a este
modo de red). El endpoint MCP está en `<addr>/mcp`.

El endpoint admite la mitad de streaming de Streamable HTTP: un `GET`
con `Accept: text/event-stream` abre un flujo de escucha para las
notificaciones del servidor, y las respuestas pueden enviarse como
Server-Sent Events. Un proxy inverso delante del servidor no debe almacenar en
búfer las respuestas `text/event-stream` (en nginx, `proxy_buffering off`); de
lo contrario, los eventos llegan tarde o no llegan.

```bash
nodered-mcp serve --transport http --http-addr :8090
```

### Autenticación del transporte HTTP

El transporte HTTP expone todas las herramientas —desplegar flujos, instalar
módulos, detener el runtime— a cualquiera que pueda alcanzar el puerto. Por
eso está protegido por un [token](glossary.md#token) bearer compartido: una
cadena secreta que el cliente envía con cada solicitud y que el servidor
comprueba.

```bash
nodered-mcp serve --transport http --http-addr :8090 --http-token "$(openssl rand -hex 32)"
```

Los clientes lo envían como una cabecera `Authorization` normal:

```
Authorization: Bearer ***
```

**El token es obligatorio siempre que la dirección de escucha sea alcanzable
desde fuera de la máquina, y el servidor se niega a arrancar sin uno.**
El caso que esto detecta es `:8090`: parece local, pero se enlaza a todas las
interfaces. Un enlace a loopback —`127.0.0.1:8090`, `localhost:8090`,
`[::1]:8090`— no necesita token, de modo que el desarrollo local no se
complica.

```bash
nodered-mcp serve --transport http --http-addr :8090
# nodered-mcp: loading config: MCP_HTTP_TOKEN is required: ":8090" is reachable
# from outside this machine ...
```

La comparación del token es de tiempo constante, así que un intento
equivocado no revela cuánto del token era correcto, y el token nunca aparece
en una respuesta. Las solicitudes rechazadas se registran con la dirección de
quien las hizo.

Aquí sigue sin haber cifrado de transporte: en una red no fiable,
colócalo detrás de un proxy inverso que termine TLS. Los clientes web
alojados que necesitan OAuth en lugar de un token fijo se tratan en la
siguiente sección.

## OAuth 2.1 (alternativa al bearer token)

Úsalo en lugar del bearer token solo si tu organización ya ejecuta un
proveedor de identidad. Los conectores web que ya hablan OAuth 2.1 / OpenID
Connect (claude.ai, interfaces personalizadas) pueden identificarse con un JWT
(un token firmado emitido por ese proveedor) en lugar de un secreto
compartido. Configura el emisor (issuer) y la audiencia (audience) y elimina
el bearer token:

```bash
MCP_TRANSPORT=http
MCP_HTTP_ADDR=:8090
MCP_OAUTH_ISSUER=https://your-idp.example/
MCP_OAUTH_AUDIENCE=nodered-mcp
```

Al arrancar, el servidor obtiene
`<issuer>/.well-known/openid-configuration` para descubrir el endpoint JWKS
y luego verifica el `Authorization: Bearer ***` de cada solicitud contra las
claves de firma del emisor. `iss` debe coincidir con el emisor configurado;
`aud` debe coincidir con la audiencia configurada. El descubrimiento ocurre
una sola vez al arrancar, así que una URL mal configurada falla de inmediato
en lugar de hacerlo en la primera llamada autenticada.

Las claves de firma se guardan en caché durante 15 minutos. Un token cuyo
`kid` no está en la caché provoca una obtención adicional, porque el
proveedor de identidad pudo haber rotado las claves; esa obtención adicional
ocurre como máximo una vez por minuto y la comparten las solicitudes que
fallan al mismo tiempo, de modo que una ráfaga de tokens no válidos no puede
convertirse en una ráfaga de descargas contra el proveedor de identidad. Una
clave rotada se detecta, por tanto, en cuanto se abre la ventana, lo que puede
tardar hasta un minuto después de la rotación.

La ventana es por proceso, así que una flota de réplicas permite una
actualización por réplica y minuto. Mientras el proveedor de identidad no
está disponible y la caché ha caducado, cada solicitud que llega después de
una obtención fallida lo intenta de nuevo por su cuenta; las solicitudes que
estaban esperando cuando falló comparten ese resultado en lugar de hacer cola
detrás de él.

Configurar a la vez `MCP_HTTP_TOKEN` y `MCP_OAUTH_ISSUER` es un error de
configuración y el servidor se niega a arrancar.
