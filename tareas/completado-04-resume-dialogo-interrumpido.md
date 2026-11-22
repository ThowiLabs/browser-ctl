# Tarea 04 - Resume de paso interrumpido por dialogo (completado)

## Objetivo
Cuando un dialogo JS interrumpe un paso (por ejemplo un click), responder el dialogo debe reanudar ese paso, como hace el JS original (hasta 10 s).

## Que se hizo (2026-11-22)
- `internal/cdp/cdp.go`: Registry que enruta eventos CDP de una conexion de navegador a los clientes de sus pestanas (Register/Unregister/Dispatch/DispatchDetached). Hallazgo: sin el, ningun evento CDP llegaba al motor en vivo (el relay ya despachaba a `Browser.Events`, pero nadie lo ponia).
- `internal/backend/backend.go`: NoteBrowser crea el Registry; Claim registra el cliente.
- `internal/engine/capture.go`: `Page.javascriptDialogOpening` guarda `ts.Dialog`; `Page.javascriptDialogClosed` lo limpia.
- `internal/engine/engine.go`: runStep corre el paso en goroutine y lo compite contra apertura de dialogo (poll 50 ms) y timeout; si abre un dialogo, el paso queda como abandoned y el batch se detiene. preStep reanuda el paso abandonado cuando el dialogo ya no esta y bloquea los demas pasos mientras hay dialogo abierto. stepWhileDialog (tab, browser, help, dialog siempre; wait solo como sleep plano), stepTimeout, finishAbandoned (max 10 s).
- `internal/engine/misc.go`: doDialog valida accept/dismiss, falla si no hay dialogo abierto, responde con Page.handleJavaScriptDialog y reanuda el paso interrumpido.
- `internal/engine/commands.go`: WhileDialog en tab y browser; ayuda de dialog documenta que un dialogo termina el batch en el que abre.
- Tests: `internal/engine/dialog_test.go` (captura de eventos, label, finishAbandoned, stepTimeout, stepWhileDialog, formatSeconds) y `internal/cdp/registry_test.go` (dispatch por pestana, unregister, detach).

## Aceptacion verificada en vivo (2026-11-22, Chrome real, https://the-internet.herokuapp.com/javascript_alerts)
- click "Click for JS Alert" -> `alert dialog "I am a JS Alert" opened`, el resto del batch no corre; `dialog accept` -> el texto pasa a "You successfully clicked an alert".
- click "Click for JS Confirm" -> confirm abierto; `snapshot` mientras esta abierto es rechazado (`confirm dialog "I am a JS Confirm" is open...`); `dialog dismiss` -> "You clicked: Cancel".
- `go vet ./...` limpio, `go test ./...` en verde (cdp, daemon, engine).
