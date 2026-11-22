# Tarea 03 — Resolución de locators por ARIA (pendiente)

## Objetivo
Que la traducción de locators a JavaScript use el árbol de accesibilidad
para resolver getByRole/getByLabel/getByText y compañía, en vez de la
traducción aproximada actual en `internal/engine/targets.go`.

## Criterio de aceptación
- `snapshot` + `click getByRole button "Enviar"` funciona en una página
  real con varios botones.
- Pruebas unitarias del resolver.
