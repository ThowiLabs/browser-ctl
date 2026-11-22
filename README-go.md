# browser-ctl v2

CLI y servidor MCP para controlar Chrome desde la terminal o desde cualquier
cliente que hable MCP, sin depender de Node.js. Un solo binario estático en Go.

Reescritura desde cero del port v1, fiel al original en JavaScript
(`browser-mcp-ext`): las mismas 31 órdenes, el mismo formato de salida y la
misma semántica de sesiones. La fuente de la extensión de Chrome no cambia.

## Requisitos

- Go 1.23 o superior (solo para compilar).
- Chrome con la extensión de browser-ctl instalada y conectada al relay.

## Compilar

```bash
go build -trimpath -o browser-ctl ./cmd/browser-ctl
```

## Uso

```bash
# Daemon en segundo plano (relay 127.0.0.1:9010-9012, sesiones en memoria)
browser-ctl daemon

# Ejecutar órdenes (crea el daemon si no existe)
browser-ctl run 'goto https://example.com'
browser-ctl run 'snapshot'
browser-ctl run 'click e12'

# Varias órdenes desde archivo
browser-ctl run --commands-file cmds.txt

# Pestañas y estado
browser-ctl tabs
browser-ctl status

# Servidor MCP por stdio (una herramienta `browser`, argumento `commands`)
browser-ctl mcp [--caps cdp]

# Parar el daemon
browser-ctl shutdown
```

## Órdenes

goto, back, forward, reload, click, hover, drag, scroll, fill, type, press,
select, check, uncheck, upload, wait, dialog, tab, browser, snapshot, find,
get, screenshot, pdf, download, eval, console, requests, request, cdp, help.

El `goto` navega la pestaña actual de la sesión y solo crea una pestaña nueva
cuando la sesión no tiene ninguna. No busca pestañas por URL, así que no crea
duplicados.

## Estructura

- `cmd/browser-ctl/` — punto de entrada y subcomandos.
- `internal/daemon/` — daemon: relay WebSocket, sesiones, descubrimiento.
- `internal/engine/` — motor de las 31 órdenes (parser, snapshot, acciones).
- `internal/mcp/` — servidor MCP por stdio y cliente JSON-RPC.
- `internal/relay/` — protocolo con la extensión (hello/welcome, pairing).
- `internal/cdp/` — cliente CDP mínimo.
- `internal/backend/` — navegadores conectados y pestañas.
- `internal/client/` — cliente del daemon para `run`, `tabs`, `status`.
- `contexto/` — decisiones y estado del proyecto, un archivo por hito.
- `tareas/` — trabajo pendiente, en proceso y completado.

## Decisiones

- Sin Node.js: binario Go estático, una sola dependencia externa
  (`gorilla/websocket`, la librería estándar no tiene cliente/servidor WS).
- Sin interfaces de una sola implementación ni capas que solo delegan
  (metodología ponytail-development).
- Los commits van en español. La rama de trabajo en el Dell es `master`
  (repositorio existente, no se reinicia el historial).

## Probar

```bash
go vet ./...
go test ./...
```

La prueba en vivo con Chrome real está en
`tareas/en-proceso-01-probar-en-vivo-dell.md`.
