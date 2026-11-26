# Documentación de nodered-mcp

[English](../README.md) | **Español**

Este es el índice de todas las páginas de documentación de `nodered-mcp`. El
[`README.es.md`](../../README.es.md) de la raíz es la puerta de entrada breve:
qué es el proyecto, cómo instalarlo y lo básico sobre seguridad. Las páginas
de abajo profundizan en cada tema.

## Contenido

- [Empieza aquí](#empieza-aquí)
- [Guías](#guías)
- [Referencia](#referencia)
- [Histórico](#histórico)

## Empieza aquí

| Página | Quién debería leerla |
|---|---|
| [`getting-started.md`](getting-started.md) | Cualquier persona que no conozca MCP ni Node-RED. Responde: ¿qué es esto?, ¿qué necesito?, ¿cómo lo instalo y hablo por primera vez con mis flujos? |
| [`glossary.md`](glossary.md) | Cualquier persona que encuentre una palabra desconocida en esta documentación. Responde: ¿qué significan aquí «flujo», «herramienta», «transporte» o «token»? |

## Guías

| Página | Quién debería leerla |
|---|---|
| [`clients.md`](clients.md) | Cualquier persona que conecte una app de IA (Claude, Cursor, VS Code, Gemini CLI, OpenCode, Pi) a Node-RED. Responde: ¿qué pego en la configuración de mi cliente? |
| [`configuration.md`](configuration.md) | Cualquier persona que quiera ajustar el servidor. Responde: ¿qué variables de entorno y flags de línea de comandos existen y cuáles son sus valores por defecto? |
| [`troubleshooting.md`](troubleshooting.md) | Cualquier persona cuya instalación no funciona. Responde: ¿por qué no aparecen las herramientas o por qué recibo un 401, y cómo me recupero? |
| [`transports.md`](transports.md) | Cualquier persona que ejecute el servidor para más de un cliente o a través de la red. Responde: ¿en qué se diferencian stdio y streamable HTTP, y cómo protejo el endpoint HTTP (bearer token, OAuth 2.1)? |

## Referencia

| Página | Quién debería leerla |
|---|---|
| [`tools.md`](tools.md) | Cualquier persona que quiera saber exactamente qué puede hacer la IA. Responde: ¿qué herramientas MCP existen (son 44) y cuáles son de lectura, escritura o acción? |
| [`architecture.md`](architecture.md) | Colaboradores y revisores. Responde: ¿cómo está organizado el código y cómo funcionan las salvaguardas de copia de seguridad antes de escribir y de tipos de nodo? |
| [`roadmap.md`](roadmap.md) | Cualquier persona que planifique en torno al proyecto. Responde: ¿qué está abierto, qué riesgos se aceptan y qué está planificado? |

## Histórico

[`audits/`](../audits/) contiene informes de auditoría anteriores (por ejemplo
[`audits/2026-07-28-v0.5.12.md`](../audits/2026-07-28-v0.5.12.md)). Son un
registro de lo que era cierto en su momento, no se actualizan y están solo en
inglés.

La fuente de verdad para el número de herramientas es
`internal/mcp/tools_test.go` (`totalTools`, `readOnlyTools`). Los números de
esta documentación se mantienen sincronizados con esas constantes.
