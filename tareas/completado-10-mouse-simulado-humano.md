# Tarea 10 - Mouse simulado humano (completado)

## Objetivo
Que el cursor simulado se vea y se mueva como el del MCP original, y más natural: como lo haría un humano.

## Alcance
- Overlay igual al JS: flecha SVG azul grande con brillo + etiqueta en pastilla, en shadow DOM cerrado (ya no se cuela en innerText).
- Movimiento humano: trayectoria curva (bezier cuadrática), ease-in-out (lento-rápido-lento), duración según distancia (250-700 ms), el ratón real sigue la misma ruta con mouseMoved.
- Click: pausa breve al llegar antes de pulsar. Drag: el overlay se mueve al inicio y al punto de soltar (antes no se movía en drag).
- Sin aleatoriedad (determinista, testeable). Sin nuevos paquetes ni dependencias.

## Resultado (verificado en vivo 2026-11-22)
- Hover: flecha grande con etiqueta visible sobre el avatar; el :hover real dispara (caption "name: user1" en snapshot -i y en la captura). `get text` ya no incluye "browser-ctl".
- Drag A→B intercambia el contenido (eval "B|A"). Click en example.com navega a iana.org.
- Bug arreglado: el primer movimiento de sesión no emitía mouseMoved (el :hover no disparaba); ahora sí.
- Observación: una pestaña extra apareció en la secuencia de prueba (misma firma que el hallazgo B, sin atribuir); la reutilización de goto se verificó aparte (dos goto, una pestaña).
- `go vet` y `go test` limpios en el Dell y en local. Repo limpio al final.

## Criterio de aceptación (cumplido)
- `go vet` y `go test ./...` limpios en el Dell.
- En vivo: hover muestra la flecha grande con etiqueta en el objetivo; drag A→B sigue intercambiando el contenido; click navega; `get text` ya no incluye "browser-ctl".
- Captura de pantalla con el cursor visible como evidencia.
- Commit en español. Repo limpio al final.
