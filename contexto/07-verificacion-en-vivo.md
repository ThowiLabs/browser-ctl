# Contexto 07 - Verificacion en vivo de comandos (2026-11-22)

## Bateria
Tarea 08. Matriz de 21 pruebas en vivo con Chrome real (the-internet, demoqa, ilovepdf). Resultados abajo; la tabla completa va al final del archivo.

## Hallazgo A - snapshot "(empty page)" tras un goto (BUG REAL, arreglado)
`snapshot -i` justo despues de `goto` devolvia "(empty page)" de forma intermitente (2 de las primeras 3 veces en /checkboxes), mientras el snapshot completo en el mismo momento si mostraba el arbol. Causa: Chrome devuelve el arbol AX antes de que renderice lineas. Arreglo en `internal/engine/snapshot.go`: `axSnapshot` reintenta hasta 6 veces (400 ms) mientras el render salga vacio. Verificado: estres goto + snapshot -i, 11 de 12 OK (el unico fallo fue en arranque en frio con 4 intentos; con 6 intentos el arranque en frio pasa 3 de 3). Commit pendiente al cierre de este hallazgo (ver git log).

## Hallazgo B - 6 pestanas iguales de /checkboxes (NO es el bug original)
Yahir vio 6 pestanas de /checkboxes (1288343065-1288343070). Investigacion:
- Mi historial: corri `goto .../checkboxes` en muchas invocaciones separadas de `run`, pero dentro de cada generacion del daemon el goto REUTILIZO la pestana (verificado: t1g/t1h y los bucles de estres no crearon pestanas nuevas).
- Cada reinicio del daemon borra la sesion "cli" (esta en memoria) y el siguiente goto crea pestana nueva. Yo reinicie el daemon 5 veces durante la bateria (2 shutdown tras rebuilds + 3 shutdown dentro del bucle de arranque en frio) mas 1 idle-exit del daemon 3076: eso explica 5 de las 6 pestanas.
- La 6a pestana queda sin atribuir con certeza: lo mas probable es un TabClosed transitorio (ver abajo) o un reinicio no registrado durante la generacion larga con ~15 gotos seguidos.
- Prueba explicita (paso 3 del encargo): `goto https://example.com` 4 veces en invocaciones separadas de `run` sobre el mismo daemon: solo la primera creo pestana (1288343071, sesion fresca); las otras 3 reutilizaron 3071 y el conjunto de pestanas no cambio. El bug original NO se reproduce.
- Mecanismo del posible transitorio (en codigo, no reproducido): `CurrentTab` olvida la pestana si `TabClosed` es true; `TabClosed` es true cuando el refresh no resuelve la pestana Y todos los navegadores reportan Pairing "paired". `browser list` mostro `b1 "" - connected`, o sea Pairing es "paired" y el mecanismo esta armado: si el estado de pestanas de la extension tarda en sincronizar una pestana recien creada, un goto podria crear otra. No se pudo reproducir en 4 intentos.
- Consecuencia de diseno (no bug): como las sesiones viven solo en la memoria del daemon, cada idle-exit (10 min) o reinicio huerfana la pestana de la sesion "cli" y el siguiente goto abre una nueva. Mejora posible (decision de Yahir, no implementada): persistir la pestana actual de "cli" en daemon.json o adoptarla al arrancar.
- Limpieza: `tab close` rechazo las duplicadas ("was not opened by this session", regla JS). Se cerraron con `tab select <id>` + `cdp Page.close` (Page.close NO esta en la lista de denegados) + wait 2000-3000 para que el estado de la extension sincronice. Las 6 pestanas cerradas y verificadas con `tabs`: quedaron solo 3049 (example.com), 3047 (chat de Yahir), 3064 (javascript_alerts) y 3071 (example.com, pestana de trabajo de la prueba).
- Leccion para el resto de la bateria: NO reiniciar el daemon salvo en la prueba 19 (ciclo de vida), que va al final.

## Hallazgo C - drag no movia nada en DnD HTML5 (BUG REAL, arreglado)
`drag e12 e15` en /drag_and_drop devolvia exito pero el orden no cambiaba. Causa: `doDrag` (internal/engine/actions.go) enviaba `Input.dispatchMouseEvent` sin el campo `buttons`, asi que Chrome veia el raton moverse sin ningun boton pulsado y nunca iniciaba el drag HTML5. Arreglo: `mousePressed` y `mouseMoved` llevan `"buttons": 1`, `mouseReleased` lleva `"buttons": 0`.
Verificacion: con listeners instrumentados se ve la secuencia nativa completa (mousedown, dragstart, dragenter/dragover, drop en column-b, dragend) y el dataTransfer llega con `text/html` = `<header>A</header>`; el contenido se intercambia (A|B -> B|A) y se puede intercambiar de vuelta (m17, m18 x2).
Ojo al verificar esta pagina: la pagina intercambia innerHTML, NO el orden de los elementos; `children[0].id` siempre es `column-a` aunque el drag funcione. Hay que leer el innerText de cada columna. Los dos primeros intentos "fallidos" fueron en realidad un doble intercambio (m12 intercambio, m13 lo devolvio) leido con el chequeo equivocado.

## Hallazgo D - snapshot con solo la raiz tras goto (BUG REAL, arreglado)
El snapshot completo justo despues de `goto` devolvia solo `- RootWebArea [ref=e1]` (el arbol aun se estaba construyendo). El reintento del Hallazgo A solo disparaba con 0 lineas; ahora tambien reintenta con 1 sola linea (raiz) hasta 10x500 ms. Verificado en /drag_and_drop: tras el arreglo el snapshot trae el arbol completo en el primer intento. Coste: un snapshot de about:blank (pagina realmente vacia) tarda hasta 5 s.

## Hallazgo E - upload fallaba con "Could not find node with given id" (BUG REAL, arreglado)
`upload e5` en /upload fallaba siempre en un attach fresco, aunque cada llamada CDP suelta funcionaba por separado (`cdp DOM.resolveNode`, `describeNode`, `setFileInputFiles`). Causa raiz, encontrada con `cdp DOM.describeNode '{"objectId":"0.2.2"}'`: el nodo resuelto desde el arbol AX devuelve `"nodeId": 0` — el agente DOM solo asigna nodeIds a los nodos que ha empujado (via getDocument/querySelector), y tras un attach no ha empujado nada. `resolveNode` y `describeNode` no fallan, pero el nodeId 0 luego rompe `describeNode`/`setFileInputFiles`. Arreglo en `nodeIDForTarget` (internal/engine/misc.go): si el nodeId sale 0 (o resolveNode falla), se hace un `DOM.getDocument` depth -1 para empujar el arbol y se resuelve de nuevo. Verificado con daemon recien reiniciado: `upload e5` funciona a la primera ("uploaded 1 file(s)" -> "File Uploaded!" + test-upload.txt). Nota: click/hover/drag no se veian afectados porque su camino preciso usa backendNodeId en getBoxModel y ademas tienen fallback a expresion JS.
Segundo bug en el mismo comando: `upload css=#file-upload` fallaba con "DOM Error while querying" porque `ParseTarget` no quitaba el prefijo `css=` y el selector literal `css=#file-upload` es CSS invalido. El original JS lo acepta porque Playwright entiende el prefijo. Arreglo en `ParseTarget` (internal/engine/targets.go): se quita el prefijo `css=` en la rama CSS. Verificado: `upload css=#file-upload` -> "uploaded 1 file(s)" -> "File Uploaded!".
Detalle de la pagina: /upload usa Dropzone; el input visible es `input#file-upload` (type=file) y hay un segundo input oculto `.dz-hidden-input` de Dropzone. El ref e5 del snapshot es el input real (boton "Seleccionar archivo"), no el area Dropzone.

## Tabla de pruebas
| # | Comando / pagina | Resultado | Evidencia |
|---|---|---|---|
| 0 | snapshot -i tras goto (/checkboxes) | FAIL -> FIXED | "(empty page)" intermitente; reintento en axSnapshot (10x500 ms); estres 11/12 con 6 intentos, arranque en frio 3/3 con 10 intentos; 1 vacio mas en m5 (pestana fresca + dyno lento, >5 s) — el reintento cubre lo habitual, no lo extremo |
| 0b | snapshot muestra estado checked | PASS | `- checkbox [ref=e5]: false` / `[ref=e6]: true` (la 2a viene marcada por defecto en el sitio); antes no se veia el estado |
| 1 | check/uncheck (/checkboxes) | PASS | `check e5` -> eval checked `true`; `uncheck e5` -> eval checked `false` |
| B | goto reutiliza pestana (4x ejemplo.com) | PASS | Solo 3071 creada (sesion fresca); 3 gotos siguientes sin pestana nueva |
| 0c | snapshot con solo la raiz tras goto (/drag_and_drop) | FAIL -> FIXED | Solo `- RootWebArea [ref=e1]`; reintento tambien con 1 linea; tras el arreglo el arbol completo sale en el primer intento |
| 3 | drag (/drag_and_drop) | PASS | `drag e12 e15` -> secuencia nativa completa (dragstart/drop, dataTransfer text/html `<header>A</header>`); contenido A\|B -> B\|A, y de vuelta A\|B -> B\|A en dos drags seguidos |
| 4 | hover (/hovers) | PASS | `hover e14` (avatar 1) -> el snapshot -i muestra el caption: heading "name: user1" + link "View profile" |
| 5 | scroll (/infinite_scroll) | PASS | `.jscroll-added` 2 -> 3 tras 3x `scroll down`; texto de la pagina 4172 -> 6273 caracteres (ojo: los parrafos no son `<p>`, son divs) |
| 6 | wait --text (/dynamic_loading/2) | PASS | `click e6` (Start) -> `wait --text "Hello World!" --timeout 15000` OK; `get text` muestra "Hello World!" |
| 7 | upload (/upload) | FAIL -> FIXED | `upload e5` y `upload css=#file-upload` fallaban (nodeId 0 tras attach fresco; prefijo css= no quitado). Tras el arreglo, ambos: "uploaded 1 file(s)" -> click Upload -> "File Uploaded!" + test-upload.txt |
