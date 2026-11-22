# Fecha
2026-11-22

# Objetivo
Reescribir browser-ctl desde cero en Go, sin Node.js, manteniendo todas las
funciones del original en JavaScript (browser-mcp-ext) y la fuente de la
extensión de Chrome sin cambios. El resultado debe funcionar como CLI y como
servidor MCP, con un solo binario estático.

# Decisiones tomadas
- Un solo binario Go estático (`CGO_ENABLED=0`), módulo
  `github.com/thovilabs/browser-ctl`, Go 1.23.
- Daemon de larga vida con sesiones en memoria (como el daemon Node del
  original): relay en 127.0.0.1:9010-9012, descubrimiento por `daemon.json`,
  salida por inactividad a los 10 minutos, `POST /shutdown`.
- Servidor MCP por stdio con JSON-RPC escrito a mano, sin librería MCP: una
  sola herramienta `browser` con argumento `commands`.
- El `goto` es por sesión: navega la pestaña actual de la sesión y solo crea
  una pestaña si la sesión no tiene ninguna. No busca pestañas por URL.
  Así desaparece el bug de pestañas duplicadas del port v1 (que buscaba por
  URL con comparación exacta y fallaba con la barra final y los redirects).
- Snapshot por CDP `Accessibility.getFullAXTree`, no lista DOM.
- Metodología ponytail-development: simplicidad agresiva pero segura, sin
  interfaces de una sola implementación ni capas que solo delegan.
- Una sola dependencia externa: `gorilla/websocket` v1.5.3 (la librería
  estándar no tiene cliente/servidor WebSocket).
- Código de relay/protocolo probado del port v1 reutilizado como base
  (`internal/protocol`, `internal/relay`, `internal/cdp`).

# Arquitectura actual
- `cmd/browser-ctl/` — main y subcomandos: daemon, mcp, run, tabs, status,
  shutdown, serve (modo relay-only heredado).
- `internal/daemon/` — daemon, sesiones, `daemon.json`, `/client`, `/shutdown`.
- `internal/engine/` — las 31 órdenes, parser, targets, snapshot, acciones,
  captura de consola/requests, exposición y política CDP.
- `internal/mcp/` — servidor MCP stdio y cliente JSON-RPC.
- `internal/relay/` — hello/welcome ECDSA, pairing, relay WebSocket.
- `internal/cdp/` — cliente CDP mínimo (métodos, eventos, sesiones).
- `internal/backend/` — navegadores conectados, pestañas, locks.
- `internal/client/` — cliente del daemon para run/tabs/status.

# Librerías usadas
- `github.com/gorilla/websocket` v1.5.3 (única dependencia externa).
- Librería estándar de Go para todo lo demás.

# Archivos importantes modificados
- `DESIGN.md` — diseño completo de la v2.
- `go.mod`, `go.sum` — módulo y dependencia.
- `internal/engine/commands.go` — registro y pre-validación de las 31 órdenes.
- `internal/engine/parser.go` — parser (args entrecomillados, corchetes, eval).
- `internal/engine/snapshot.go` — snapshot por árbol de accesibilidad.
- `internal/daemon/run.go` — daemon, sesiones, `/client`, `/shutdown`.

# Problemas encontrados
- `Daemon.Start` no guardaba el servidor: pánico nil en `Stop`. Corregido.
- Lotes solo con `help` esperaban 20 s en `WhenReady`: el JS lo salta.
  Corregido.
- `run` devolvía el JSON crudo del resultado MCP en vez del texto.
  Corregido en `internal/client/client.go` (2026-11-22, antes del envío).
- Los `capabilities` (`--caps cdp`) no llegaban a la sesión del daemon.
  Corregido en `internal/daemon/run.go` + `Session.AddCapabilities`
  (2026-11-22, antes del envío).
- Código muerto en `Backend` (mapa de refs e IDs spare sin uso).
  Eliminado; quedó marcado como deuda técnica en el código.

# Soluciones implementadas
- Hitos 1-3: daemon, MCP stdio, CLI, sesiones, goto por sesión.
- Hitos 4-5: refs de snapshot con node IDs de DOM, refs de iframe
  `f<idx>e<n>`, overlay de cursor en click/hover, buffers de consola y
  requests desde eventos CDP, `get count`, `upload` con
  `DOM.setFileInputFiles`, `cdp on/events/wait`.
- Pruebas: `go vet` limpio, `go test ./...` en verde, prueba de integración
  con extensión falsa (daemon + hello/welcome + goto + segundo goto reusa
  la pestaña), MCP stdio de extremo a extremo con el binario real.

# Pendientes
- Prueba en vivo en el Dell con Chrome real y la extensión.
- Instalar como browser-ctl v2 en el repositorio del Dell.
- Resolución de locators por ARIA (hoy aproximada por el árbol AX).
- Resume real de pasos interrumpidos por diálogos (hoy es un stub).
- IDs públicos estables de pestañas (spare >= 100001).
- Decisión de pairing: auto-pair como el fork o flujo real de 6 dígitos.

# Próximos pasos
1. Enviar el ZIP al Dell, reemplazar el código v1 y probar allí.
2. Prueba en vivo: daemon + goto + snapshot + click en Chrome real.
3. Instalar y hacer commit en español en el repositorio del Dell.
