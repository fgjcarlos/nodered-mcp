# Herramientas

[English](../tools.md) | **Español**

Esta página es el catálogo de todo lo que el servidor permite hacer a un
asistente de IA con tu Node-RED: qué [herramientas](glossary.md#herramienta-tool)
existen, cuáles solo miran y cuáles cambian cosas, y las reglas de seguridad
que se aplican a las que cambian cosas. La necesitas si quieres saber qué pedir
a tu asistente, si ejecutas el servidor en
[modo solo lectura](glossary.md#modo-solo-lectura), o si tienes que decidir si
es seguro permitir una operación concreta.

## Contenido

- [Cómo leer esta página](#cómo-leer-esta-página)
- [Política de tipos de nodo](#política-de-tipos-de-nodo)
- [Seguridad de los reintentos](#seguridad-de-los-reintentos)
- [Presupuestos de tiempo](#presupuestos-de-tiempo)
- [Límites de la recuperación de copias de seguridad](#límites-de-la-recuperación-de-copias-de-seguridad)
- [Flujos](#flujos)
- [Paleta](#paleta)
- [Runtime, diagnóstico y recuperación](#runtime-diagnóstico-y-recuperación)
- [Recuperación del ayudante de contexto](#recuperación-del-ayudante-de-contexto)
- [Recursos (3)](#recursos-3)
- [Prompts (2)](#prompts-2)
- [Detección de la versión de Node-RED](#detección-de-la-versión-de-node-red)
- [Cómo leer `get_runtime_info`](#cómo-leer-get_runtime_info)

## Cómo leer esta página

Una **herramienta** (tool) es una acción que el asistente de IA puede pedir al
servidor que realice, como «listar mis flujos» o «instalar un nodo». Tú no
llamas a las herramientas directamente: escribes una petición normal en el chat
de tu asistente y el asistente elige la herramienta. Cada herramienta tiene un
nombre fijo (por ejemplo `list_flows`); los ejemplos de cada grupo más abajo
muestran el tipo de frase que lleva a ella. Consulta el
[glosario](glossary.md) para palabras como [flujo](glossary.md#flujo-flow),
[pestaña](glossary.md#pestaña-tab), [nodo](glossary.md#nodo) y
[API de administración](glossary.md#api-de-administración).

44 herramientas, 3 recursos, 2 prompts. Las herramientas se clasifican por
riesgo:

- **read** (lectura) — sin efectos secundarios (solo mira; no cambia nada).
- **write** (escritura) — modifica la configuración persistida (cambia lo que
  está guardado en Node-RED).
- **action** (acción) — tiene un efecto secundario en el runtime que no se
  persiste (p. ej. `inject_node`).

En las tablas siguientes, **HTTP** es la llamada a la
[API de administración](glossary.md#api-de-administración) que hace la
herramienta (`local` significa que nunca contacta con Node-RED), **Risk**
(riesgo) es una de las tres clases anteriores y **Notes** (notas) recoge los
detalles que conviene conocer.

Una escritura que cambia la configuración de flujos hace antes una instantánea
(una [copia de seguridad](glossary.md#copia-de-seguridad-backup)) de ella; las
escrituras de la paleta e `inject_node` no. Consulta
[Qué escrituras guardan una instantánea de la configuración de flujos](#qué-escrituras-guardan-una-instantánea-de-la-configuración-de-flujos).

La separación `read` / `write` se aplica en el **registro** de las
herramientas, no dentro de cada gestor: las 23 herramientas que modifican no
se anuncian cuando se usa `--read-only`, de modo que un modelo no puede llamar
a lo que no ve. `inject_node` cuenta como una herramienta que modifica:
disparar un inject puede enviar una orden real a hardware real.

Las 21 herramientas marcadas como `read` son las únicas registradas con
`--read-only`.

## Política de tipos de nodo

En resumen: el servidor se niega a crear o cambiar nodos de ciertos tipos
peligrosos (por defecto `exec` y `system`, que pueden ejecutar comandos en el
equipo anfitrión). La lista es la
[lista de bloqueo](glossary.md#lista-de-bloqueo-denylist) (denylist).

Toda herramienta de escritura que pueda llevar un tipo de nodo está sujeta a la
política `MCP_NODE_DENYLIST` (por defecto `exec,system`), que no distingue
mayúsculas de minúsculas e ignora los espacios al principio y al final.
La política se juzga
sobre la configuración efectiva que desplegaría la escritura, y el documento se
recorre de forma recursiva, de modo que un tipo prohibido anidado dentro de la
definición de un subflujo también se rechaza. `update_node` se juzga sobre el
nodo tal como queda después de combinar el parche. El rechazo nombra
`MCP_NODE_DENYLIST` y el tipo infractor. Ocurre antes de hacer ninguna
instantánea de copia de seguridad y, en toda superficie cuyo contenido efectivo
se conoce de antemano, antes de contactar con el runtime. Dos superficies no
pueden conocerlo de antemano: `update_node` y `connect_nodes` se juzgan sobre lo
que producirían, así que su rechazo sigue a una lectura de la pestaña y nunca a
un despliegue ni a una copia de seguridad.

`add_node`, `update_node`, `create_flow`, `update_flow`, `import_flow` y
`set_flows` también comprueban los tipos enviados contra la paleta del runtime
antes de desplegar. El registro exige el nombre exacto. Se aceptan los tipos
estructurales (`tab`, `group`, `junction`, `subflow`) y las instancias de
subflujos existentes. Si no se puede leer la paleta, se rechaza la escritura.
El cliente guarda la paleta en caché y la invalida tras cambios exitosos desde
MCP; reinicia el servidor si cambias la paleta fuera de MCP.

Superficies cubiertas por la lista de bloqueo: `create_flow`, `update_flow`, `add_node`,
`update_node`, `connect_nodes`, `import_flow`, `set_flows`,
`restore_backup`, `create_subflow`, `update_subflow`,
`instantiate_subflow`. El propio ayudante `set_context` del servidor obedece la
misma política. Consulta [`SECURITY.md`](../../SECURITY.md).

## Seguridad de los reintentos

En resumen: mirar se reintenta automáticamente, cambiar no, para que un cambio
nunca se aplique dos veces por accidente.

Las herramientas de lectura reintentan los fallos tipados de transporte HTTP
hasta dos veces (tres intentos en total), con un plazo de 15 segundos por
intento. Los errores de validación y las respuestas de error HTTP completas no
se reintentan, sea cual sea su texto.

Las herramientas de escritura y de acción se ejecutan una sola vez. Si una
conexión se cierra o un plazo vence sin una respuesta completa, la herramienta
informa de un **resultado indeterminado**: la acción puede haberse ejecutado
ya. Inspecciona el runtime de Node-RED antes de repetir la operación; un error
final no prueba que no haya cambiado nada. Las copias de seguridad protegen la
recuperación de la configuración, no frente a mensajes duplicados ni a
comandos de hardware. Esta política rige las repeticiones automáticas de los
gestores MCP; el reintento existente de `set_context` tras un HTTP 404
explícito relacionado con el aprovisionamiento no cambia.

### Ediciones concurrentes

Las herramientas granulares (`add_node`, `update_node`, `delete_node`,
`connect_nodes`, `disable_flow`, `enable_flow`, `instantiate_subflow`) leen la
configuración completa una sola vez, con la revisión que le corresponde, y
despliegan esa misma revisión. Por tanto, la pestaña que parchea la edición es
la pestaña que lleva el despliegue, y de ello se siguen tres cosas:

- un cambio que otro editor hizo en una propiedad que la edición no toca
  sobrevive a la edición: la edición se aplica sobre el estado actual, no sobre
  una copia más antigua;
- un despliegue que llega después de la instantánea es rechazado por el runtime
  con un **409**, y la edición no se repite sobre él;
- un conflicto es un conflicto: la herramienta lo notifica en lugar de
  sobrescribir el trabajo del otro editor. Vuelve a leer el flujo antes de
  reintentar.

## Presupuestos de tiempo

En resumen: cada llamada tiene un límite de tiempo; las operaciones lentas,
como instalar un nodo, tienen uno más largo.

Cada intento de herramienta se ejecuta bajo un plazo de contexto (context
deadline). El valor por defecto es de 15 segundos. Las herramientas cuyo
trabajo legítimamente dura más tienen su propio presupuesto, de modo que el
plazo que fija el envoltorio MCP no corta la operación:

| Herramientas | Presupuesto | Por qué |
|---|---|---|
| `install_node`, `uninstall_node` | 5 minutos | npm se ejecuta por debajo y tarda minutos |
| `set_flows`, `set_flows_state`, `restore_backup` | 2 minutos | un despliegue completo, un reinicio del runtime o una restauración pueden bloquear una instancia ocupada |
| todas las demás herramientas | 15 segundos | el valor por defecto |

El servidor añade un pequeño margen sobre esos presupuestos, de modo que el
presupuesto que realmente recibe el trabajo del runtime es el indicado arriba y
no el tiempo que la llamada pasó esperando el bloqueo de escritura.

El presupuesto es el del servidor, no el del cliente: muchos clientes MCP
abandonan una llamada tras su propio tiempo límite, así que una instalación de
cinco minutos solo dura lo que el cliente esté dispuesto a esperar.

Un cliente puede cancelar una llamada en cualquier momento. La cancelación
llega al runtime: la petición en curso se aborta. Lo que el runtime haga con la
operación después depende de él (una instalación de npm que ya ha empezado
sigue ejecutándose), así que una operación cancelada nunca se repite
automáticamente.

Cancelar una llamada significa que el cliente deja de esperar; no significa que
el servidor pueda comunicar el resultado a un cliente que se ha ido. Una
escritura que se cancela mientras se ejecuta informa de un resultado
indeterminado si quien llamó sigue escuchando; una escritura cancelada mientras
aún espera el bloqueo de escritura informa de que no se inició ninguna acción;
una lectura cancelada informa de un intento fallido.

Mientras una operación larga de paleta mantiene el bloqueo de escritura, una
escritura que llega durante ella se rechaza cuando vence su propio plazo. El
rechazo indica que no se inició ninguna acción, y nada llega al runtime.

Dos bloqueos protegen una escritura, y ambas esperas son cancelables: el
bloqueo propio del servidor para el ayudante de contexto y el bloqueo de
escritura del cliente que serializa los endpoints que modifican. Una escritura
que llega mientras uno de ellos está ocupado se rinde cuando su contexto
termina (cancelado por quien llama o fuera de su propio plazo), y se rinde
antes de hacer ninguna instantánea o de contactar con el runtime. El rechazo
nombra el bloqueo que estaba esperando:

- `waiting for context helper lock: context canceled; no action was started`
- `waiting for write lock: context canceled; no action was started`

## Límites de la recuperación de copias de seguridad

En resumen: las [copias de seguridad](glossary.md#copia-de-seguridad-backup)
automáticas restauran la *forma* de tus flujos, pero no las contraseñas ni
otros secretos que guarda Node-RED. Lee esto antes de borrar nada.

Las copias de seguridad automáticas son **instantáneas estructurales de
`GET /flows`**, no copias de seguridad completas de recuperación ante desastres
de Node-RED. `restore_backup` restaura únicamente el JSON de los flujos. No
respalda ni recupera el almacén de credenciales cifradas independiente de
Node-RED ni las claves/configuración necesarias para descifrarlo. La API de
administración no expone los secretos ocultos para que este servidor los
exporte.

**Antes de borrar nodos o pestañas, o de reemplazar flujos**, organiza una
recuperación externa autorizada. Node-RED puede eliminar sus credenciales
cuando se borran; restaurar los mismos IDs de nodo desde una instantánea no
puede reconstruir esos secretos. Por ejemplo, restaurar un broker MQTT borrado
puede recuperar el nodo pero no su usuario/contraseña. Un `restore_backup`
correcto no prueba que se hayan recuperado la autenticación ni el
funcionamiento del runtime. Restaurar una instantánea puede, por sí mismo,
eliminar nodos y credenciales; la instantánea automática previa a la
restauración también es estructural.

### Procedimiento de recuperación externa

1. Antes de la operación destructiva, detén Node-RED y haz una copia de
   seguridad consistente y autorizada de su directorio de usuario. Incluye el
   JSON de flujos correspondiente, el archivo de credenciales cifradas
   (`flows_cred.json`, o el equivalente del archivo de flujos configurado),
   `settings.js` y `.config.runtime.json` cuando contenga la clave de cifrado
   generada. Conserva también el `credentialSecret` original y cualquier
   secreto/configuración suministrado externamente. Protege esta copia como
   material sensible; no la envíes a través de MCP.
2. Reinicia Node-RED después de la copia. Para una recuperación completa
   posterior, deténlo de nuevo y restaura los archivos externos
   correspondientes con su clave/configuración de cifrado original, y luego
   reinicia. Es una recuperación gestionada por el operador, **no** algo que
   realice `restore_backup`.
3. Si no existe una copia de las credenciales ni su clave correspondientes,
   restaura la estructura, vuelve a introducir las credenciales en el editor de
   Node-RED y despliega. Comprueba la autenticación y el funcionamiento de los
   nodos afectados antes de dar la recuperación por terminada.

El servidor de lectura y escritura envía el aviso completo en sus instrucciones
de inicialización MCP. Las herramientas de escritura de la configuración de
flujos siguen advirtiendo en sus descripciones de que las copias de seguridad
omiten las credenciales, aunque el cliente no muestre las instrucciones del
servidor. `delete_flow` y `restore_backup` repiten el aviso completo en sus
respuestas correctas.

Las herramientas de paleta e `inject_node` no anuncian estos límites: la paleta
cambia los módulos instalados e `inject_node` lanza un mensaje, y ninguna
cambia la configuración de flujos. `set_context` tampoco los anuncia, aunque
aprovisionar su pestaña auxiliar sea una escritura de la configuración de
flujos: la herramienta escribe en el contexto del ayudante, y solo
aprovisionar la pestaña auxiliar toca los flujos. No se concede al servidor MCP
ningún acceso adicional al sistema de archivos ni a secretos.

### Qué escrituras guardan una instantánea de la configuración de flujos

La instantánea falla de forma segura (fail-closed): cuando no se puede
escribir, la escritura no continúa.

**Guardan una instantánea de la configuración actual:**

- pestañas de flujo: `create_flow`, `update_flow`, `delete_flow`,
  `enable_flow`, `disable_flow`;
- ediciones granulares: `add_node`, `update_node`, `delete_node`,
  `connect_nodes`;
- el portapapeles del editor: `import_flow`;
- despliegue completo y restauración: `set_flows`, `restore_backup`;
- definiciones de subflujo: `create_subflow`, `update_subflow`,
  `delete_subflow`, `instantiate_subflow`;
- el estado del runtime: `set_flows_state` (inicia o detiene el runtime y
  guarda una instantánea de la configuración antes de hacerlo);
- el ayudante `set_context`, cuando la llamada tiene que aprovisionar su
  pestaña.

**No guardan ninguna instantánea:**

- la paleta: `install_node`, `uninstall_node`, `enable_node`,
  `disable_node`: cambian los módulos instalados, no la configuración de
  flujos;
- `inject_node`: lanza un mensaje; no se persiste nada.

## Flujos

Un [flujo](glossary.md#flujo-flow) (una [pestaña](glossary.md#pestaña-tab) en
el editor) es un conjunto de [nodos](glossary.md#nodo) conectados; un
[subflujo](glossary.md#subflujo-subflow) es un grupo reutilizable de nodos.
Estas herramientas los leen y los editan.

Ejemplos de peticiones:

- «Muéstrame todos mis flujos» usa `list_flows`.
- «Encuentra todos los nodos que hablan con MQTT» usa `search_flows`.
- «Crea un flujo nuevo llamado Jardín» usa `create_flow`.
- «Añade un nodo debug al flujo Jardín» usa `add_node`.
- «¿Es válido este flujo antes de desplegarlo?» usa `validate_flow`.
- «Exporta el flujo Jardín para poder compartirlo» usa `export_flow`.

`get_flow`, `export_flow`, `update_flow`, `delete_flow`, `disable_flow` y
`enable_flow` requieren un identificador de pestaña. Pasa `flow_id` o el `id`
original. Si pasas ambos, deben coincidir; los conflictos se rechazan antes de
contactar con Node-RED. Para `disable_flow` y `enable_flow`, el id también debe
ser un único segmento de ruta: no puede contener `/`, `\` o `%`, ni ser `.` o `..`.
Las herramientas de nodo como `add_node` siguen usando `flow_id` para la pestaña,
y `get_context` y `set_context` siguen usando `id` para los ámbitos de flujo y de nodo.

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `list_flows` | `GET /flows` | read | Resumen por defecto, `detail="full"` es opcional |
| `search_flows` | `GET /flows` | read | |
| `get_flow` | `GET /flow/:id` | read | |
| `create_flow` | `POST /flow` | write | |
| `update_flow` | `GET /flows` + `POST /flows` | write | Reescritura completa; prefiere las herramientas granulares; la pestaña escrita se juzga en el contexto de la configuración completa |
| `delete_flow` | `DELETE /flow/:id` | write | |
| `set_flows` | `POST /flows` | write | Despliegue completo, el más destructivo; `force=true` omite solo la validación de referencias estructurales; la sintaxis del JavaScript de los nodos function siempre se comprueba |
| `add_node` | `GET /flows` + `POST /flows` | write | Confirma si se conservaron las conexiones de salida suministradas; sugiere `connect_nodes` solo cuando no hay conexiones de salida |
| `update_node` | `GET /flows` + `POST /flows` | write | Combina propiedades |
| `delete_node` | `GET /flows` + `POST /flows` | write | Limpia las conexiones entrantes |
| `connect_nodes` | `GET /flows` + `POST /flows` | write | Rechaza un puerto igual o superior al número de salidas del origen (`outputs` de function y trigger, reglas de switch), un `port` que no sea un número entero de 0 a 999, y una conexión desde un nodo sin salidas (debug, http response, link out, mqtt out), hacia uno sin entradas (inject, catch, status, link in) o que toque un nodo de configuración, nombrando el nodo y el motivo; una conexión que ya existe se responde como ya conectada, sin despliegue ni copia de seguridad |
| `validate_flow` | local | read | Comprobaciones en seco (dry-run) de estructura y de sintaxis del JavaScript de nodos function, más advertencias consultivas para ciertos nodos del núcleo. `func`, `initialize` y `finalize` se analizan como cuerpos de función asíncrona; los problemas de sintaxis indican el nodo, el campo y la ubicación. Un array plano se juzga como un todo: ids únicos entre pestañas, subflujos y nodos de configuración, un tipo en cada entrada y un propietario (pestaña o subflujo) para cada nodo y grupo. Una instancia de un subflujo que no está en la carga útil no es un problema estructural. Una conexión desde un nodo sin salidas, hacia uno sin entradas o que toca un nodo de configuración se notifica como `impossible_wire`, un problema estructural, tanto en el documento de la pestaña como en el array plano |
| `disable_flow` | `GET /flows` + `POST /flows` | write | |
| `enable_flow` | `GET /flows` + `POST /flows` | write | |
| `inject_node` | `POST /inject/:id` | action | Excluida de `--read-only`; carga útil opcional |
| `export_flow` | `GET /flows` | read | Formato de portapapeles del editor: un array plano con la pestaña (`"type":"tab"`) primero, luego sus nodos, y los nodos de configuración globales y las definiciones de subflujo que usan (de forma transitiva) |
| `import_flow` | `GET /flows` + `POST /flow` | write | Toma el portapapeles plano del editor (exactamente un elemento `tab`; la forma anidada `[{...tab, nodes}]` todavía se acepta). Los ids se conservan salvo que ya exista alguno en la instancia; entonces todos los ids de nodo de la copia se vuelven a generar y se reescriben sus conexiones y referencias. Los subflujos que la instancia tiene se reutilizan; un nodo de configuración global se reutiliza solo cuando la instancia lo tiene como nodo de configuración global del mismo tipo; en caso contrario se crea con ámbito en la nueva pestaña (con un id regenerado cuando su id ya está en uso); un subflujo ausente se rechaza (créalo antes con `create_subflow`). Un elemento con coordenadas de lienzo (`x`/`y`) pero sin `z` se rechaza. La política de tipos de nodo juzga cada elemento del portapapeles antes de contactar con el runtime, incluidos los miembros de las definiciones de subflujo y los nodos de configuración globales que acaban reutilizándose, ya que una instancia de un subflujo reutilizado ejecuta los miembros que tiene la instancia. Una ruta `http in` (método + url, con `/` inicial añadida como hace Node-RED) de la copia que un nodo `http in` habilitado en una pestaña habilitada ya sirve se importa igualmente, y el resultado nombra la ruta, ese nodo y su pestaña: borrar la copia más tarde también anula el registro de la ruta original hasta que esa pestaña se vuelva a desplegar (`disable_flow` + `enable_flow`, o cualquier despliegue de esa pestaña) |
| `list_subflows` | `GET /flow/global` | read | |
| `get_subflow` | `GET /flow/global` | read | |
| `create_subflow` | `PUT /flow/global` | write | Primero se valida la topología de la definición |
| `update_subflow` | `PUT /flow/global` | write | Primero se valida la topología de la definición |
| `delete_subflow` | `PUT /flow/global` | write | Rechaza mientras la definición siga instanciada |
| `instantiate_subflow` | `GET /flows` + `POST /flows` | write | |

### Eliminar la definición de un subflujo

`delete_subflow` se niega a borrar una definición que todavía está
instanciada. Un nodo de instancia (un nodo de tipo `subflow:<id>`, en una
pestaña de flujo o anidado dentro de otra definición de subflujo) es lo único
que mantiene viva una definición, y el runtime resuelve ese tipo en el momento
del despliegue: si se elimina la definición de todos modos, la instancia sigue
apuntando a un tipo inexistente, el runtime registra `Waiting for missing types
to be registered` en el siguiente arranque, y los flujos afectados nunca se
despliegan.

El rechazo nombra todas las referencias que hay que eliminar primero, y la
definición queda intacta. Las instancias se eliminan con `delete_node` (una
instancia en una pestaña de flujo) o reescribiendo la definición que las
contiene con `update_subflow` (una instancia anidada en otro subflujo);
`delete_subflow` nunca las elimina por ti.

### Qué valida un despliegue

Un despliegue instala una configuración completa, así que se comprueba como un
todo antes de enviar nada al runtime. La comprobación es local: nada llega a
Node-RED y no se hace ninguna copia de seguridad cuando la configuración se
rechaza.

Las reglas son las que aplica el runtime al mismo array (con una excepción,
indicada más abajo), y cada una es una referencia que no se resuelve:

- **una conexión (wire) que no nombra ningún nodo de la misma pestaña (o del
  mismo subflujo)**: es la regla de este repositorio, compartida con la ruta de
  escritura por pestaña, y es más estricta que la del runtime: el runtime sí
  resuelve una conexión entre pestañas (recurre a la búsqueda global de nodos),
  de modo que esa conexión entrega. El editor nunca crea una, y ambas rutas de
  escritura la rechazan, de modo que la configuración que instala un operador y
  la configuración que luego puede editar se juzgan con la misma regla;
- **un nodo cuyo `z` no nombra ni una pestaña ni una definición de subflujo**:
  el runtime inventa una pestaña para él, y el nodo se ejecuta fuera del flujo
  que se desplegó;
- **dos entradas con el mismo id**: el runtime indexa cada nodo por su id, así
  que gana la última entrada y la anterior desaparece sin avisar;
- **una instancia de un subflujo cuya definición no está en la
  configuración**: el runtime instala un tipo de nodo que no puede resolver,
  registra `Waiting for missing types to be registered` y deja los flujos
  detenidos.

Dos topologías se dejan deliberadamente en paz, porque pertenecen al runtime:
los **nodos link** (el runtime recalcula las conexiones de un nodo `link out` a
partir de su array `links`, y un `link in` puede apuntar a un `link out` de
otra pestaña: para eso sirve un enlace) y las **instancias de subflujo** (una
instancia es un nodo ordinario de su pestaña, y sus conexiones se comprueban
contra esa pestaña como las de cualquier otro nodo).

`set_flows` aplica estas reglas al array que recibe, y `restore_backup` las
aplica a la copia de seguridad que va a instalar. Una copia de seguridad que ya
no valida (una sacada de un runtime que tenía una referencia rota, o editada a
mano) se rechaza con la lista de problemas; `restore_backup` con `force=true`
la instala de todos modos, y el resultado indica que se instaló sin validar.
Esa es la vía de recuperación, y hay que pedirla de forma explícita. `force`
omite solo las comprobaciones de referencias estructurales: el JavaScript no
válido de un nodo function se sigue rechazando antes de una copia de seguridad
o un despliegue.

`set_flows` tiene la misma vía de escape estructural (`force=true`), por la
misma razón: las reglas son las del repositorio, y una de ellas es más estricta
que la del runtime. La política de tipos de nodo y la regla de que un
despliegue debe dejar al menos una pestaña **no** se omiten con ella: no tratan
de las referencias de la configuración sino de lo que un despliegue puede hacer
al runtime. La validación de la sintaxis de las funciones sigue siendo
obligatoria con o sin force.

`validate_flow` y las comprobaciones de despliegue rechazan el uso de
`import()` dinámico dentro de un nodo Function. Node-RED 5.0.10 reconoce la
sintaxis, pero el entorno aislado del nodo no proporciona la función que
necesita para cargar un módulo de esa manera. Para usar un módulo, agrégalo
en la pestaña **Setup** del nodo Function y usa la variable configurada en
tu código. Esto requiere que `functionExternalModules` esté habilitado en
Node-RED.

Esa regla de la pestaña se decide decodificando el `type` de cada entrada, no
buscando texto en la carga útil. El mismo documento se comporta igual tanto si
llega como una cadena compacta, como una cadena con sangrías o como un array de
objetos, y un `type: "tab"` mencionado dentro de una propiedad (un objeto
anidado o un fragmento de texto) no es una pestaña.

`update_flow` escribe una sola pestaña, y se juzga en el contexto de la
configuración completa: las entradas de la pestaña que escribe son de quien
llama, y las reglas que necesitan la configuración completa (una instancia de
un subflujo cuya definición no está) se aplican a ellas. El resto de la
configuración es estado propio del runtime, y un problema ahí no es algo de lo
que deba responder quien llama: rechazarlo haría imposible reparar el runtime.

`create_subflow` y `update_subflow` comparten la guarda que la ruta de
escritura de pestañas siempre ha aplicado: una conexión dentro de la
definición que nombra un nodo que no está en ella, y un nodo cuyo `z` nombra
otra pestaña, se rechazan antes de contactar con el runtime. Una definición de
subflujo también se juzga por sus puertos: el runtime resuelve los puertos de
un subflujo contra los miembros de la propia definición, así que un puerto `in`
que nombra un nodo que no está hace que el runtime lance una excepción cuando
arranca una instancia de ese subflujo, y un puerto `out` que nombra uno registra
`Unknown node referenced inside subflow` y la salida nunca se dispara. La única
referencia legal que no es un miembro es el propio id de la definición en un
puerto `out`: así es como un subflujo conecta su entrada directamente a su
salida.
Toda conexión de un puerto `out` también debe llevar un `port` que sea un
entero no negativo (`{"id":"fn","port":0}`): el runtime indexa las conexiones
del destino por él, de modo que un `port` ausente o no válido, o uno que supera
las salidas del nodo interno (tantas como tiene su array `wires`), nunca se
dispara, y en un paso directo al propio id de la definición un `port` que no
sea menor que el número de puertos `in` hace que el arranque lance una
excepción. Esto se notifica como `invalid_subflow_port` en `create_subflow`,
`update_subflow`, `set_flows`, `restore_backup` y `validate_flow`. Las
conexiones de un puerto `in` llevan solo un `id`.

### Advertencias semánticas de `validate_flow`

`ok` e `issues` siguen describiendo la validación estructural. El array
opcional `warnings` es consultivo y nunca afecta a las escrituras. Informa de:

- `output_count_mismatch`: el número de grupos en `wires` difiere de las
  salidas declaradas de un nodo Function o Trigger, o del número de reglas de
  un Switch cuando está disponible. Los grupos vacíos cuentan como ranuras de
  salida.
- `invalid_property_type`: el `pt` de una regla Change (o el `tot` de destino
  de una regla Move) no es `msg`, `flow` ni `global`, o el `propertyType` de un
  Switch no es uno de esos tipos, `jsonata` o `env`. Esto no comprueba los
  tipos de valor de comparación ni de literales.
- `http_response_unreachable`: no se alcanza ninguna HTTP Response desde un
  HTTP In siguiendo conexiones y enlaces estáticos en el documento
  suministrado. Un camino a través de un subflujo, un Link Call o un enlace a
  un destino ausente no es concluyente y suprime la advertencia. Esto no
  garantiza una respuesta en tiempo de ejecución ni comprueba si hay varias
  respuestas alcanzables.

Las comprobaciones son locales; no consultan al runtime ni inspeccionan
definiciones de nodos aportadas por módulos.

## Paleta

La [paleta](glossary.md#paleta) es la lista de tipos de nodo disponibles en el
editor. Estas herramientas la consultan, buscan más en el registro público de
npm, y los instalan, eliminan, habilitan o deshabilitan.

Ejemplos de peticiones:

- «¿Qué nodos están instalados?» usa `list_nodes`.
- «¿Hay un nodo para Telegram?» usa `search_nodes`.
- «Instala node-red-contrib-influxdb» usa `install_node`.
- «Desactiva ese módulo por ahora» usa `disable_node`.

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `list_nodes` | `GET /nodes` | read | |
| `get_node_info` | `GET /nodes/:module` | read | |
| `search_nodes` | npm registry | read | Registro público (`registry.npmjs.org`) |
| `install_node` | `POST /nodes` | write | |
| `uninstall_node` | `DELETE /nodes/:module` | write | |
| `enable_node` | `PUT /nodes/:module[/:set]` | write | |
| `disable_node` | `PUT /nodes/:module[/:set]` | write | |

Si `install_node` devuelve HTTP 404, usa `search_nodes` para encontrar el nombre
correcto del paquete de npm antes de volver a intentarlo.

Cuando Node-RED devuelve su respuesta genérica `unexpected_error` / `Install
failed` para cualquiera de las dos operaciones de paleta, el error remite a los
operadores a la salida del runtime de Node-RED (stdout, `journalctl`,
`docker logs` o un gestor de logs configurado en `settings.js`), donde se
escriben los detalles de npm. Las causas habituales incluyen scripts de ciclo
de vida que fallan y una versión de Node incompatible. Una instalación manual
en el directorio de usuario con `npm install --ignore-scripts <module>` puede
servir como solución alternativa; verifica antes que el paquete es de
confianza. Omitir los scripts de ciclo de vida puede dejar algunos paquetes
inutilizables.

## Runtime, diagnóstico y recuperación

Estas herramientas miran cómo está funcionando Node-RED en sí (ajustes, logs,
salida de depuración, valores de [contexto](glossary.md#contexto)), inician o
detienen los flujos, y listan o restauran
[copias de seguridad](glossary.md#copia-de-seguridad-backup).

Ejemplos de peticiones:

- «¿Qué versión de Node-RED es esta y está sana?» usa `get_diagnostics` y `get_runtime_info`.
- «¿Qué imprimieron los nodos debug en los últimos minutos?» usa `get_debug_messages`.
- «Muéstrame los logs de Node-RED» usa `get_runtime_logs`.
- «¿Qué hay guardado en la variable global `counter`?» usa `get_context`.
- «Muéstrame mis copias de seguridad» usa `list_backups`.
- «¿Qué cambió entre esa copia de seguridad y ahora?» usa `diff_flows`.
- «Vuelve a la copia de seguridad de esta mañana» usa `restore_backup`.

| Tool | HTTP | Risk | Notes |
|---|---|---|---|
| `get_settings` | `GET /settings` | read | |
| `get_diagnostics` | `GET /diagnostics` | read | Requiere Node-RED ≥3.1 |
| `get_flows_state` | `GET /flows/state` | read | Requiere Node-RED ≥3.0.0; no depende de `runtimeState` |
| `get_context` | `GET /context/...` | read | editor-api, sin contrato de estabilidad |
| `set_context` | `GET /flows`, `POST /flow`, `POST /inject/:id` | write | Ayudante inject/function verificado y persistente |
| `get_debug_messages` | `/comms` WebSocket | read | Búfer de 500, se reconecta |
| `get_runtime_logs` | journal / stream | read | |
| `list_plugins` | `GET /plugins` | read | editor-api |
| `get_runtime_info` | complemento de `get_diagnostics` | read | Vista del servidor MCP sobre el runtime |
| `get_node_status` | `/comms` WebSocket | read | |
| `set_flows_state` | `POST /flows/state` | write | Requiere `settings.runtimeState.enabled: true` |
| `list_backups` | local | read | |
| `diff_flows` | local + `GET /flows` | read | |
| `restore_backup` | `POST /flows` | write | Primero se valida estructuralmente la copia de seguridad; `force=true` omite solo las comprobaciones estructurales, no la validación de sintaxis de funciones |

## Recuperación del ayudante de contexto

`set_context` descubre su ayudante en `GET /flows` antes de cada llamada,
incluidas las llamadas posteriores a reiniciar el proceso MCP. Un ayudante
admitido tiene una única pareja aislada inject/function con el código de
función, la configuración y las conexiones esperados; la etiqueta de la
pestaña por sí sola no prueba su identidad. Un ayudante válido se reutiliza sin
un despliegue de configuración. La denylist de nodos configurada también se
aplica a la reutilización.

Cuando no existe ningún ayudante válido, el servidor crea una pestaña completa
en un único `POST /flow` protegido, con IDs de nodo que no colisionan con la
configuración existente. No hay despliegues intermedios de pestañas vacías. Una
respuesta fallida o ambigua se devuelve sin repetición automática; una llamada
explícita posterior descubre primero cualquier ayudante que el runtime haya
instalado realmente. Las pestañas existentes que estén editadas, incompletas o
sin relación se dejan intactas en lugar de borrarse o sobrescribirse.

Borrar o reemplazar el ayudante mediante `delete_flow`, `set_flows`, una
restauración de copia de seguridad o el editor no invalida de forma permanente
al servidor MCP: la siguiente llamada descubre un ayudante válido o aprovisiona
uno nuevo. La recuperación no migra el contexto de flujo/nodo del ayudante
anterior; usa `list_flows` para descubrir los IDs actuales de esos ámbitos. El
contexto global sigue perteneciendo a Node-RED, no a la pestaña auxiliar.

Las escrituras de este servidor MCP se serializan con la verificación del
ayudante y el envío del inject. Las sesiones independientes del editor, otros
procesos MCP y los clientes externos de la API de administración no comparten
ese bloqueo: evita ediciones o aprovisionamientos concurrentes del ayudante
desde esos clientes. No es una transacción distribuida ni un singleton entre
procesos.

## Recursos (3)

Un [recurso](glossary.md#recurso) (resource) es un dato de solo lectura que el
asistente puede abrir directamente, sin llamar a una herramienta.

| URI | Descripción |
|---|---|
| `nodered://flows/current` | La configuración completa actual de flujos |
| `nodered://settings` | Ajustes del servidor |
| `nodered://flows/state` | Estado del runtime |

## Prompts (2)

Un [prompt](glossary.md#prompt) es una plantilla de petición ya preparada que
el cliente puede ofrecerte, por ejemplo «explica este flujo».

| Nombre | Descripción |
|---|---|
| `explain_flow` | Describe qué hace un flujo, sus disparadores y sus dependencias externas |
| `generate_flow` | Construye un flujo a partir de una descripción en lenguaje sencillo |

## Detección de la versión de Node-RED

Algunas herramientas necesitan una versión mínima de Node-RED: `set_context` e
`inject_node` con carga útil necesitan 5.0.0, `get_diagnostics` necesita
3.1.0 y `get_flows_state` necesita 3.0.0. El MCP lee la versión de
`GET /settings` al arrancar e informa de estos requisitos en
`get_runtime_info`. Para `set_context` e `inject_node` con carga útil, el MCP
también comprueba la versión antes de la operación, de modo que esa
comprobación no depende de que una llamada anterior haya calentado la caché.

Conviene conocer dos propiedades:

- **Un runtime local se detecta como cualquier otro.** La dirección del
  runtime (`http://localhost:1880` es la predeterminada) no cambia la
  comprobación.
- **Una sonda fallida no es definitiva.** Si el runtime no es alcanzable cuando
  arranca el MCP, la versión queda desconocida hasta el siguiente intento, y
  una operación posterior sujeta a versión vuelve a sondear.

Cuando no se puede detectar la versión, `get_runtime_info` informa de la
capacidad como `unknown`. Si Node-RED deniega el acceso a `/settings` con `401`
o `403`, la herramienta devuelve en su lugar un error que indica qué
credenciales configurar (consulta [Solución de problemas](troubleshooting.md)).
Las llamadas que comprueban la versión **no** se
rechazan solo por ese motivo. Su mensaje de éxito lleva el sufijo "Node-RED
version was not detected; this call was not verified against the minimum
version".

## Cómo leer `get_runtime_info`

La respuesta incluye dos mapas. `capabilityMatrix` es el estado de cada
herramienta frente al runtime al que está conectado este MCP;
`capabilityGuidance` explica las que no están en `ok`.

```json
{
  "mcp": {
    "capabilityMatrix": {
      "set_context": "version_too_low",
      "get_diagnostics": "ok",
      "set_flows_state": "setting_disabled"
    },
    "capabilityGuidance": {
      "set_context": {
        "reason": "Node-RED 3.0.0 is below the 5.0.0 minimum required by set_context",
        "remedy": "Upgrade Node-RED to at least 5.0.0"
      },
      "set_flows_state": {
        "reason": "the runtime-state setting did not read as enabled; settings.runtimeState.enabled was false, absent, or /settings was unreadable",
        "remedy": "Set settings.runtimeState.enabled to true in settings.js (or via the runtime settings UI) and restart Node-RED"
      }
    }
  }
}
```

Las entradas `ok` **no** tienen entrada de guía: la ausencia es la señal de que
la herramienta funciona, así que un cliente solo tiene que leer el mapa cuando
algo está degradado.

`unknown` es un estado real, no un eufemismo: significa que no se pudo detectar
la versión de Node-RED, así que la capacidad está genuinamente indeterminada.
Lleva una razón y deliberadamente **ningún** remedio, porque no hay ninguna
solución conocida que recomendar. Una herramienta de `/comms` que todavía no se
ha observado informa `stream_unknown` en su lugar, para que cada estado
conserve un único significado.

Cada razón se deriva de una sonda que el gestor ya ejecutó. Nada de esto se
infiere, y no se toca ningún endpoint que modifique para producirlo. La razón de
versión nombra el mínimo real de esa herramienta concreta, así que
`set_context` (necesita 5.0.0) y `get_diagnostics` (necesita 3.1.0) dan
consejos distintos.

Algunos estados tienen más de una causa posible y las sondas no pueden
distinguirlas, así que la razón expone la observación en lugar de un veredicto.
`setting_disabled` cubre por igual una compuerta cerrada por el operador, una
clave de ajuste ausente y un `/settings` ilegible.

Las dos herramientas que leen el WebSocket `/comms` (`get_node_status` y
`get_debug_messages`) se clasifican a partir de **dos** cosas: la activación
opcional `MCP_DEBUG_STREAM` (configuración) y lo que el MCP observó del
WebSocket (disponibilidad). Un indicador activado por sí solo nunca se informa
como `ok`:

- `stream_disabled`: la activación está desactivada: `MCP_DEBUG_STREAM` no se
  definió.
- `ok`: la última conexión observada del tail fue correcta.
- `stream_disconnected`: el tail intentó conectar y falló, o perdió una
  conexión que tenía; la razón lleva el último error.
- `stream_unavailable`: no existe tail: el endpoint `/comms` no pudo derivarse
  de la URL de Node-RED configurada.
- `stream_unknown`: el tail todavía no ha informado de ninguna conexión: aún
  está conectando, o el intento se queda colgado contra un host inalcanzable.

Cada una de las dos herramientas se clasifica a partir de su propio tail, así
que una puede estar en `ok` mientras la otra está en `stream_disconnected`.

`ok` es lo último que observó el MCP, no una comprobación de vida: una
partición de red puede dejar un socket a medio abrir que todavía parece
conectado.
