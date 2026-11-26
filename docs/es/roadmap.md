# Hoja de ruta

[English](../roadmap.md) | **Español**

Esta página muestra en qué punto está el proyecto: qué preguntas de diseño ya
están resueltas, qué añadió cada versión publicada y qué se planea a
continuación. Está pensada para quien decide si adoptar o actualizar el
servidor, y para quienes contribuyen y buscan temas abiertos. Para la lista del
día a día de lo que puedes pedirle a tu asistente, consulta
[Herramientas](tools.md); para términos como
[herramienta](glossary.md#herramienta-tool),
[modo solo lectura](glossary.md#modo-solo-lectura) o
[lista de bloqueo](glossary.md#lista-de-bloqueo-denylist), consulta el
[glosario](glossary.md).

## Contenido

- [Riesgos abiertos y decisiones](#riesgos-abiertos-y-decisiones)
- [Versiones entregadas](#versiones-entregadas)
- [Planificado](#planificado)

## Riesgos abiertos y decisiones

| Tema | Estado |
|---|---|
| Modelo de datos con pérdida de campos | ✅ Resuelto: JSON opaco |
| Copia de seguridad antes de escribir | ✅ Implementado, falla de forma segura (fail-closed) |
| Estrategia de edición con un LLM | ✅ Resuelto en v0.5: edición granular por defecto, reescritura completa como vía de escape |
| WebSocket `/comms` | ✅ Implementado con reconexión y búfer acotado |
| Transporte HTTP junto a stdio | ✅ Implementado |
| Autenticación Bearer en el transporte HTTP | ✅ Token obligatorio en enlaces que no son de loopback; el servidor se niega a arrancar sin él |
| OAuth para clientes web | ✅ Servidor de recursos OAuth 2.1 / OIDC con descubrimiento de JWKS |
| **Concurrencia (dos LLM editando)** | ✅ Resuelto: una edición granular se construye sobre la instantánea con revisión que despliega, de modo que un despliegue que llegue entre medias se rechaza con un 409 en lugar de sobrescribirlo |
| **Poda de copias de seguridad** | ✅ Implementado: se conservan las `NODERED_BACKUP_KEEP` instantáneas más recientes (50 por defecto, `0` desactiva la poda) |
| Tamaño del búfer de depuración | ⬜ Fijo en 500; será configurable si alguien llega al límite |
| Caché local de consultas | ⬜ No planificado |

## Versiones entregadas

| Versión | Alcance | Estado |
|---|---|---|
| v0.1 | 10 tools, 1 resource, 2 prompts, transporte stdio | Publicada |
| v0.2 | Transporte Streamable HTTP, CLI con flags y subcomandos | Publicada |
| v0.3 | Gestión de la paleta: instalar, desinstalar, habilitar, deshabilitar | Publicada |
| v0.4 | `search_nodes`, ajustes y estado del runtime — 19 tools, 3 resources, 2 prompts | Publicada |
| v0.5 | Modo solo lectura, lecturas eficientes en contexto, diagnósticos, contexto, el stream de depuración, edición granular de nodos, `diff_flows`, autenticación bearer en HTTP — 43 tools | Publicada |
| v0.6 | Servidor de recursos OAuth 2.1 para conectores web alojados; canal npm con binarios para Windows; SBOM + procedencia (provenance) en cada artefacto; Dependabot; actions fijadas por SHA; CODEOWNERS + plantillas de issues/PR; detección central de la versión de NR; get_runtime_info con matriz de capacidades; banner de arranque con el número de tools degradadas — 44 tools | Publicada |
| v0.7 | Paquetes de plataforma nativos de npm: el paquete principal de npm lista los seis binarios nativos como `optionalDependencies` de versión exacta y el descargador de postinstall desaparece; publicación en npm endurecida (control de alcance, promoción serializada, toolchain de publicación fijado); `init` detecta Claude Desktop antes de que exista su configuración | Publicada (v0.7.2) |
| v0.8 | Endurecimiento: la denylist de nodos en cada superficie de escritura, despliegues validados antes de llegar al runtime, el portapapeles plano del editor en `export_flow`/`import_flow` (cambio incompatible), sin repetición automática de tools que modifican, ediciones granulares sobre una sola instantánea con revisión, bloqueo de escritura cancelable, actualizaciones de JWKS acotadas, cada canal de publicación condicionado al CI del commit etiquetado; `init --dry-run` | Publicada (v0.8.0) |
| v0.9 | Seguridad: la denylist sigue las instancias de subflujo hasta sus definiciones (GHSA-9674-fhx9-vp6c); validación de puertos de salida de subflujos; `import_flow` avisa de rutas `http in` duplicadas; guía honesta sobre capacidades y registros; el pipeline de publicación tolera la propagación lenta de npm | Publicada (v0.9.0) |
| v0.10 | Seguridad: los ids de flujos y de nodos inject ya no pueden escapar de su ruta de la API de administración; JavaScript de los nodos Function comprobado antes de cada despliegue; advertencias semánticas consultivas en `validate_flow`; `init --name` para varias instancias; alias `flow_id`; tools de lectura anotadas como solo lectura | Publicada (v0.10.0) |

## Planificado

Todavía no hay ninguna versión planificada. La caché local de consultas se
consideró para v0.7 y se descartó: la tabla de riesgos anterior la lista como
*no planificada*, y v0.7 incluyó en su lugar los paquetes de plataforma nativos
de npm.
