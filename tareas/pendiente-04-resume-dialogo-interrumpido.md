# Tarea 04 — Resume de paso interrumpido por diálogo (pendiente)

## Objetivo
Cuando un diálogo JS interrumpe un paso (por ejemplo un click), responder
el diálogo debe reanudar ese paso, como hace el JS original (hasta 10 s).

## Estado actual
Existe el canal `TabState.Abandoned` pero nada lo cierra: hoy es un stub.

## Criterio de aceptación
- Un click interrumpido por `alert` se reanuda al responder el diálogo.
- Prueba con extensión falsa o en vivo.
