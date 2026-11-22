# Fecha
2026-11-22

# Objetivo
Completar el motor con las 31 órdenes del JS original y su semántica exacta
(hitos 4 y 5), verificado con pruebas locales.

# Decisiones tomadas
- Las refs de snapshot llevan el backend DOM node id, resuelto con
  `DOM.resolveNode` + `DOM.getBoxModel`, para apuntar al elemento exacto.
- Las refs dentro de iframes son `f<idx>e<n>`, con el árbol de frames de
  `Page.getFrameTree`.
- Click y hover dibujan el overlay de cursor en la página y esperan a que
  se asiente la navegación (500 ms de vigilancia de URL + espera de carga).
- Los buffers de consola y requests se alimentan de eventos CDP
  (`Runtime.consoleAPICalled`, `Runtime.exceptionThrown`, `Log.entryAdded`,
  `Network.*`), con tope de 500 entradas y filtrado del 404 de favicon.
- `get count` cuenta el objetivo real, no las líneas de salida.
- `upload` usa `DOM.setFileInputFiles` y verifica que el objetivo sea un
  input de archivo.
- `cdp on` / `events` / `wait` guardan y consultan eventos CDP suscritos.

# Arquitectura actual
- `internal/engine/capture.go` — buffers de consola/requests desde CDP.
- `internal/engine/snapshot.go` — refs con node IDs y frames.
- `internal/engine/actions.go` — click/hover con overlay y settle.
- `internal/engine/reads.go` — console, requests, get, find.
- `internal/engine/misc.go` — upload, dialog, cdp, tab, browser.

# Librerías usadas
- Sin cambios: solo `gorilla/websocket`.

# Archivos importantes modificados
- `internal/engine/capture.go`, `capture_test.go` (nuevos).
- `internal/engine/snapshot.go`, `actions.go`, `reads.go`, `misc.go`.

# Problemas encontrados
- Sin problemas de compilación; `go vet` limpio y `go test ./...` en verde.

# Soluciones implementadas
- Hitos 4 y 5 completados y verificados el 2026-11-22.

# Pendientes
- Resolución de locators por ARIA (aproximada por el árbol AX).
- Resume de pasos interrumpidos por diálogos (stub: existe el canal
  `TabState.Abandoned` pero nada lo cierra).

# Próximos pasos
- Prueba en vivo en el Dell (ver `tareas/en-proceso-01-probar-en-vivo-dell.md`).
