# Fecha
2026-11-22

# Objetivo
Corregir dos fallos reales y limpiar código muerto antes de enviar la v2 al
Dell, siguiendo la auditoría ponytail (borrar antes que agregar).

# Decisiones tomadas
- Corregir en local y verificar con `go vet` + `go test` antes de comprimir,
  porque el envío al Dell es el punto de entrega.
- No reescribir el historial Git del Dell: el ZIP se envía sin `.git/` y en
  el Dell se reemplaza solo el árbol de trabajo, conservando su `.git/` y su
  rama `master`. El commit de la v2 va encima del historial existente.
- El trabajo continúa en el MCP del Dell a partir de este envío, no en el
  entorno local.

# Arquitectura actual
- Sin cambios de arquitectura.

# Librerías usadas
- Sin cambios.

# Archivos importantes modificados
- `internal/client/client.go` — `RunCommands` ahora devuelve el texto
  plano del resultado MCP (antes devolvía el JSON crudo) y convierte
  `isError` en error de Go.
- `internal/daemon/run.go` — `toolsCall` ahora aplica los `capabilities`
  del parámetro de la llamada a la sesión (`--caps cdp` ya llega).
- `internal/engine/engine.go` — nuevo `Session.AddCapabilities`.
- `internal/backend/backend.go` — eliminados `refs` y `nextSpare` (muertos);
  `DefaultBrowser` reutiliza `defaultLocked` en vez de duplicar la lógica.
  Deuda técnica anotada en el código: IDs públicos estables pendientes.

# Problemas encontrados
- `run` imprimía JSON crudo: la salida no era usable como CLI.
- `--caps cdp` no tenía efecto: el daemon ignoraba los capabilities.
- `Session.mu` es privado y el daemon está en otro paquete: hacía falta un
  método exportado.

# Soluciones implementadas
- `go vet ./...` limpio, `go test ./...` en verde, `go build` correcto
  después de los tres cambios.

# Problemas encontrados (en el Dell, después del envío)
- `go vet` falló en Windows: `syscall.SysProcAttr.Setsid` no existe en
  Windows (es solo Unix). La v2 se había compilado solo en Linux.
- Solución: `internal/client/detach_unix.go` (Setsid) y
  `internal/client/detach_windows.go` (CREATE_NEW_PROCESS_GROUP |
  DETACHED_PROCESS con constantes numéricas, sin dependencias nuevas).
  Verificado con `GOOS=windows go build ./...`.

# Problemas encontrados (prueba en vivo en el Dell, 2026-11-22)
- Cada `run` abría una pestaña nueva: cada invocación del CLI era una
  sesión nueva del daemon y `goto` crea pestaña cuando la sesión no tiene
  ninguna. Solución: el CLI usa sesión estable "cli"
  (`mcp.DialSession`, `hello.sessionId`); el daemon la conserva entre
  conexiones y muere con el daemon. `Engine.Run` serializa por sesión
  con `runMu` para que dos `run` concurrentes no se entrelacen.
- `snapshot` fallaba con "tab ... is not open": la extensión responde
  a `createTab` antes de registrar la pestaña. Solución:
  `Backend.CreateTab` espera hasta 5 s a que `FindTab` la vea.
- `snapshot` solo mostraba 2 líneas: `getFullAXTree` con `{"depth": 0}`
  devuelve un árbol superficial (3 nodos). Solución: omitir el
  parámetro depth para pedir el árbol completo.
- `tabs` y `run` imprimían el JSON crudo del resultado MCP. Solución:
  `client.RunCommands` desempaqueta el envelope y devuelve texto plano.
- `go vet` falló en Windows (`Setsid` no existe): archivos con build
  tags `detach_unix.go` / `detach_windows.go`.

# Pendientes
- Prueba en vivo en el Dell (repetir tras los arreglos).

# Próximos pasos
- Comprimir el proyecto completo (sin `.git/`, sin binarios) y enviarlo al
  Dell para reemplazar el código v1.
