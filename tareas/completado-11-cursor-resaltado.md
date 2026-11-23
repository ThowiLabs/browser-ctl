# Tarea 11 - Cursor y resaltado NATIVOS en la extensión

## Objetivo
Sacar el cursor del Runtime.evaluate del motor y dibujarlo el content script de la extensión (mundo aislado), y resaltar el objetivo con el dominio CDP Overlay (lo dibuja el navegador, no toca el DOM). Que sea lo menos detectable posible por antibots.

## Alcance
1. Resaltado con el dominio CDP Overlay desde el motor: Overlay.enable en Attach + Overlay.highlightNode con el backend node id de la ref (reutiliza la resolución de reads.go), fallback a Overlay.highlightRect con el box model si no hay node id; Overlay.hideHighlight al terminar la acción. Verificado: Overlay.* NO está denegado en packages/extension-core/src/background/cdp-policy.ts y NO hay lista de denegados en el Go, así que no hace falta tocar la política.
2. Cursor en el content script de la extensión (packages/extension-core/src/content.ts, entrypoint apps/extension/src/entrypoints/frames.content.ts): mismo aspecto actual (flecha SVG azul grande con brillo + pastilla "browser-ctl"), en shadow DOM cerrado, id de host ALEATORIO por carga de página, sin texto que se cuele en innerText, y que se ELIMINE del DOM cuando no se está usando. La posición la manda el background con un nuevo request `cursor` por el relay existente (protocolo v1, sin cambiar versión); el motor sigue emitiendo los mouseMoved reales por CDP en paralelo.
3. Humanización extra en el motor (internal/engine/actions.go): jitter/temblor leve, overshoot + corrección al llegar (modelo balístico-correctivo), duraciones variables; pasos aplicados en requestAnimationFrame en el content script en vez de intervalos fijos.
4. Cuando el cursor de la extensión funcione, QUITAR el cursor inyectado por Runtime.evaluate del motor (setCursorPos). Si algo falla, volver al de la tarea 10 antes de entregar roto.
5. Verificación EN VIVO con capturas reales: hover en /hovers (caption "name: user1"), click en example.com (navega a iana.org), drag A->B en /drag_and_drop, resaltado visible en captura y ausente del DOM (innerText y querySelectorAll), y pasada por una página de detección (bot.sannysoft.com o creepjs) documentando qué señales quedan y cuáles no.

## Fuera de alcance
- Cambiar el pairing (auto-pair se queda por decisión de Yahir).
- Tocar los 31 comandos (solo cambia el camino interno del cursor y el resaltado).
- Puente Playwright, tarea 09 (snapshot -i e iframes).

## Estado
- en-proceso- desde 2026-11-22 20:38 CST.
- Implementado y con checks en verde (go vet/test, vitest 3/3 cursor, typecheck 0, build WXT 0, binario reconstruido, daemon nuevo pid 6580). Verificación en vivo PARCIAL con la extensión vieja todavía cargada: hover/click/drag pasan, resaltado Overlay visible en captura y ausente del DOM (0 nodos), fallback a la tarea 10 confirmado, bot.sannysoft.com con todas las huellas estáticas en verde. Detalle en contexto/09-cursor-resaltado-nativo.md.
- PENDIENTE (bloquea el commit y el cierre): Yahir tiene que recargar la extensión reconstruida en chrome://extensions (o cargar descomprimida .output/chrome-mv3). Después: repetir el hover y comprobar que `[id^="bctl-"]` es true y que `browser-ctl-cursor` NO aparece. Solo entonces: commit en español (mensaje en C:\Users\Admin\Downloads\commit-msg.txt, borrarlo después), mover esta tarea a completado-, dejar el working tree limpio y sincronizar el espejo local.
