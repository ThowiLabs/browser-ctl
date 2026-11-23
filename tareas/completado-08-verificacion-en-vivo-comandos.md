# Tarea 08 - Verificacion en vivo de comandos (en-proceso)

## Objetivo
Probar en vivo, con Chrome real, todos los comandos y comportamientos que hasta hoy solo se probaron con extension falsa o nunca se probaron (matriz de 21 pruebas). Registrar todo en contexto/07-verificacion-en-vivo.md.

## Alcance
- the-internet.herokuapp.com: checkboxes, dropdown, drag_and_drop, hovers, infinite_scroll, dynamic_loading/2, upload, download, frames/iframe, nested_frames, javascript_error, javascript_alerts, windows, login, network_console.
- demoqa.com: upload-download, automation-practice-form, frames, droppable, select-menu, checkbox.
- ilovepdf.com: comprimir PDF y descargar resultado.
- Comportamientos: focus emulation en pestana en segundo plano, reglas de exposicion, denegacion CDP, ciclo de vida del daemon (shutdown + auto-arranque + idle-exit), MCP con cliente real.

## Criterio de aceptacion
- Tabla por prueba (1-21) con PASS/FAIL/PENDIENTE y evidencia concreta.
- Cada FAIL con bug real: arreglado (commit) o nueva tarea pendiente.
- Repo limpio al final; archivos de prueba fuera del repo.
