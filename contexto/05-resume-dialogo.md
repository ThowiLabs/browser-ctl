# Contexto 05 - Resume de dialogo interrumpido (tarea 04, 2026-11-22)

## Hallazgo principal
El motor no recibia ningun evento CDP en vivo: el relay ya despachaba cada
cdpEvent a `Browser.Events`, pero nadie asignaba ese campo, asi que los
buffers de consola/peticiones y `Page.javascriptDialogOpening` estaban
muertos con un navegador real (en el test de extension falsa se llamaba a
DispatchEvent directo y por eso no se noto). Se arreglo con un Registry
(`internal/cdp/cdp.go`): NoteBrowser lo crea, Claim registra el cliente de
la pestana, el relay despacha ahi.

## Semantica implementada (como el JS original)
- Cada paso corre en goroutine y compite contra la apertura de un dialogo
  (poll cada 50 ms) y su timeout. Si abre un dialogo, el paso queda como
  abandoned (canal en TabState), el batch se detiene y se dice como
  responderlo.
- `dialog accept|dismiss` responde con Page.handleJavaScriptDialog y
  reanuda el paso interrumpido esperando hasta 10 s a que termine.
- Mientras hay un dialogo abierto, solo corren tab, browser, help, dialog
  y `wait` como sleep plano; lo demas es rechazado con el mensaje del
  dialogo abierto.
- Un dialogo termina el batch en el que abre (documentado en la ayuda).

## Verificacion en vivo
Chrome real, https://the-internet.herokuapp.com/javascript_alerts:
- Alert: click interrumpido, `dialog accept`, texto "You successfully clicked an alert".
- Confirm: click interrumpido, `snapshot` rechazado con el dialogo abierto,
  `dialog dismiss`, texto "You clicked: Cancel".
- `go vet ./...` limpio, `go test ./...` en verde.
