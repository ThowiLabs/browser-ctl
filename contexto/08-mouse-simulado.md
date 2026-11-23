# Contexto 08 - Mouse simulado humano (2026-11-22)

## Por qué
Yahir preguntó si el mouse simulado se podía implementar como en el MCP original. Ya existía una versión simple (texto "➤ browser-ctl" que se deslizaba 0.3 s en click/hover y no se movía en drag), pero era poco visible y se colaba en innerText. El original usa flecha SVG grande con brillo + etiqueta en pastilla, en shadow DOM cerrado.

## Diseño (internal/engine/actions.go)
- `setCursorPos`: crea el overlay una vez (div#browser-ctl-cursor en `<html>`, shadow cerrado con el SVG y la etiqueta "browser-ctl") y lo mueve con `transform: translate(x, y)`.
- `moveCursor(sess, client, x, y, emit)`: recuerda la última posición en la sesión; si es el primer movimiento, coloca directo. Si no, recorre una bezier cuadrática (punto de control desplazado 8% de la distancia a un lado) con easeInOutCubic, en 8-24 pasos, duración 250-700 ms según distancia. Con emit=true el ratón real sigue la misma ruta (mouseMoved por paso); con false solo se mueve el overlay.
- Click: glide + pausa de 80 ms al llegar + press/release. Hover: glide con eventos. Drag: glide al inicio (con eventos), press, 8 pasos, glide del overlay al destino (sin eventos), release. Igual que el JS.
- Sin aleatoriedad: curva siempre al mismo lado, duraciones fijas por distancia.

## Verificación (en vivo en el Dell, 2026-11-22)
- Hover en /hovers: la flecha grande azul con brillo y la etiqueta "browser-ctl" se ve sobre el avatar (captura cursor-hover2.png); el :hover real también dispara: snapshot -i muestra heading "name: user1" + link "View profile".
- `get text` ya no incluye "browser-ctl": el shadow DOM cerrado lo saca de innerText.
- Drag en /drag_and_drop: A→B intercambia el contenido (eval "B|A").
- Click en example.com → navega a iana.org.
- Bug encontrado y arreglado en la marcha: en el primer movimiento de una sesión el overlay se colocaba pero no se emitía ningún mouseMoved, así que el :hover no disparaba. Ahora el primer movimiento también emite mouseMoved en el destino.
- Observación (sin atribuir, misma firma que el hallazgo B de la batería): en la secuencia de prueba apareció una pestaña extra (goto a example.com tras el drag creó la 3131 en vez de reutilizar la 3129). La reutilización se verificó justo después (dos goto a example.com, una sola pestaña), y este cambio no toca doGoto ni el seguimiento de pestañas.
- Un `screenshot` dio timeout de 30 s una vez y funcionó al reintentarlo (transitorio, no relacionado con el cambio).
- Limpieza: las dos pestañas de prueba se cerraron (la 3129 con select+close, era de una sesión anterior); solo queda la pestaña del chat.
