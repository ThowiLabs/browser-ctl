# Tarea 06 — Decisión de pairing (pendiente)

## Objetivo
Decidir el modelo de pairing de la v2 con Yahir.

## Opciones
- Auto-pair, como el fork de la extensión (`isPairedKey` siempre true):
  la extensión confía en cualquier cliente local sin pedir código.
- Flujo real de 6 dígitos (Allow en la extensión, como dicen los docs del
  protocolo original): más seguro, un paso manual la primera vez.

## Criterio de aceptación
- Decisión de Yahir registrada en `contexto/` y aplicada en el código.
