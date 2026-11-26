# Solución de problemas

[English](../troubleshooting.md) | **Español**

Esta página es para cuando algo no funciona: el asistente no muestra las
herramientas de Node-RED, Node-RED responde con un error, se rechaza un
cambio o la instalación falla en tu sistema. Busca el síntoma en negrita y
sigue los pasos que aparecen debajo. Un buen primer paso ante cualquier
problema es mirar la salida de registro del servidor (consulta «Sin salida de
registro» más abajo) y confirmar que el propio Node-RED está en ejecución y
accesible en la URL que configuraste (`NODERED_URL`, consulta la
[configuración](configuration.md)).

## Contenido

- [Conexión y herramientas](#conexión-y-herramientas)
- [Copias de seguridad](#copias-de-seguridad)
- [Problemas de instalación y de plataforma](#problemas-de-instalación-y-de-plataforma)

## Conexión y herramientas

**Las herramientas no aparecen.** Confirma que el binario está en el `PATH`, o
usa una ruta absoluta en `command`. En Windows, escapa las barras invertidas:
`C:\\path\\nodered-mcp.exe`. Ejecutar `nodered-mcp init` resuelve la ruta por
ti. Si tiene que recurrir al nombre del comando, asegúrate de que
`nodered-mcp` esté disponible en el `PATH` del cliente MCP. (El `PATH` es la
lista de carpetas en las que tu sistema busca programas; el
[cliente](glossary.md#cliente) MCP es la app de IA, como Claude Desktop.) En
la práctica: ejecuta `nodered-mcp version` en una terminal. Si imprime una
versión, el binario está en tu `PATH`; si dice «command not found», usa la
ruta completa en la configuración de tu cliente.

**401 o 403 de Node-RED.** Falta el [token](glossary.md#token) o no tiene el
alcance (scope) necesario. Con `adminAuth` habilitado (el inicio de sesión de
Node-RED para su [API de administración](glossary.md#api-de-administración)),
genera un token con permiso de escritura sobre los flujos.

**«Node-RED version was not detected».** El MCP lee la versión del runtime
mediante `GET /settings`. Cuando esa lectura falla —el runtime está caído,
`/settings` requiere una autenticación de administración que el token no
cubre, o un proxy elimina la ruta— la versión queda desconocida y las
herramientas condicionadas por versión (`set_context`, `inject_node` con un
payload) continúan sin comprobar la versión, y lo indican en el resultado. El
MCP reintenta la sonda en operaciones posteriores, así que un runtime que
arranca después del inicio se detecta sin reiniciar.

**Sin salida de registro.** Los registros se escriben en stderr; stdout se
reserva para las tramas JSON-RPC en el transporte [stdio](glossary.md#stdio)
(los mensajes legibles por máquina entre la app y el servidor). Aumenta el
nivel de detalle con `--log-level debug`.

**El transporte HTTP no se conecta.** (Consulta [Transportes](transports.md).)
Confirma que `--transport http` está definido y que el puerto de
`--http-addr` está libre.

## Copias de seguridad

**Se rechaza una escritura con un error de copia de seguridad.** Las copias de
seguridad son «fail-closed» por diseño (una copia de seguridad es una copia
guardada de tus flujos que se toma antes de cada cambio): si la instantánea no
se puede escribir, la escritura no continúa, de modo que nunca cambias flujos
sin una forma de volver atrás. Comprueba que `NODERED_BACKUP_DIR` existe y es
escribible.

En la imagen de contenedor el directorio es `/var/lib/nodered-mcp/backups` y
el servidor se ejecuta como el usuario `nonroot` (uid 65532), así que un bind
mount cuyos permisos no permitan escribir a ese usuario hace que falle con un
error de copia de seguridad toda llamada que genere una instantánea. Monta en
su lugar un volumen
(`--volume nodered-backups:/var/lib/nodered-mcp/backups`) y mantenlo montado
entre recreaciones.

La imagen incluye ese directorio con la propiedad del usuario `nonroot`, y
Docker copia un directorio de la imagen —su contenido y su propiedad— en un
volumen con nombre vacío, de modo que el contenedor puede escribir la
instantánea en el volumen sobre el que está montado. Un volumen que ya
contiene archivos propiedad de root (creado por otra imagen, o escrito como
root) conserva esa propiedad: vacíalo o recréalo.

## Problemas de instalación y de plataforma

**Windows: shims de npm huérfanos (heredado).** Un `npm i -g
@fgjcarlos/nodered-mcp` de una versión anterior a la 0.5.16 podía dejar tres
archivos lanzadores (`nodered-mcp`, `.cmd`, `.ps1`) en `%APPDATA%\npm\` que
ningún `npm uninstall` elimina. Tras #192 el paquete npm incluye directamente
los binarios de Windows, así que las instalaciones nuevas no reproducen esto.

Quienes heredaron los shims huérfanos de una instalación anterior a la 0.5.16
pueden eliminarlos con el script incluido:

```powershell
.\scripts\cleanup-npm-shims.ps1
```

El script es idempotente; cada Remove-Item tolera un archivo ausente, así que
volver a ejecutarlo es seguro. No requiere administrador ni modifica el PATH.

Las entradas siguientes son específicas de un sistema operativo o de un tipo
de CPU.

**macOS — «cannot be opened because the developer cannot be verified».**
Los binarios que distribuye npm no están firmados con un Apple Developer ID.
En la primera ejecución, Gatekeeper de macOS los bloquea. Haz clic derecho en
el binario en Finder → Abrir → confirma una vez, o elimina el atributo de
cuarentena para todas las invocaciones posteriores:

```bash
xattr -dr com.apple.quarantine "$(npm root -g)/@fgjcarlos/nodered-mcp"
```

Esto afecta a toda CLI sin firmar instalada con `npm install -g` (p. ej.
`esbuild`, `playwright`, `prisma`), no solo a `nodered-mcp`. La firma de código
no entra por ahora en el alcance.

**Windows ARM64 — `EPERM` o «not a valid Win32 application».**
Las máquinas Snapdragon X / Surface Pro X traen activada por defecto una capa
de emulación x64, lo que hace que npm resuelva el paquete de plataforma
`@fgjcarlos/nodered-mcp-win32-x64`. Si quieres el binario ARM64 nativo, fuerza
la instalación con `--cpu=arm64 --os=win32`:

```bash
npm install -g @fgjcarlos/nodered-mcp --cpu=arm64 --os=win32
```

**Linux ARM64 — Raspberry Pi / AWS Graviton.**
El paquete `@fgjcarlos/nodered-mcp-linux-arm64` se resuelve automáticamente en
Linux `aarch64`. Si ves «Exec format error», el binario se resolvió para la
arquitectura equivocada: comprueba `uname -m` y `npm config get os cpu`. En
Raspberry Pi OS / Debian puede que también necesites `libc6` ≥ 2.31
(Bullseye); Bookworm funciona bien.

**Linux — «GLIBC not found» / «GLIBC_2.XX not found».**
Los binarios se compilan en Ubuntu 22.04 (glibc 2.35). En distribuciones más
antiguas (Debian 10, CentOS 7, RHEL 7) el cargador no encuentra los símbolos
necesarios y el binario falla de inmediato con un error de versión de glibc.
Usa `go install` con una toolchain de Go adecuada, o ejecuta la imagen de
Docker. `npm rebuild` no ayuda porque el fallo ocurre en tiempo de ejecución,
no en el de instalación.

**Arquitectura no compatible (Linux PowerPC, FreeBSD, OpenBSD, etc.).**
npm solo distribuye los seis paquetes de plataforma que figuran en el README.
Para cualquier cosa fuera de esa matriz, usa una de estas opciones:

```bash
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
# or
docker pull ghcr.io/fgjcarlos/nodered-mcp:latest
```
