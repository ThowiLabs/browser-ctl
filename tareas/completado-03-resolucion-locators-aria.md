# Tarea 03 — Resolución de locators por ARIA (completado)

## Objetivo
Que la traducción de locators a JavaScript use el árbol de accesibilidad
para resolver getByRole/getByLabel/getByText y compañía, en vez de la
traducción aproximada actual en `internal/engine/targets.go`.

## Criterio de aceptación
- `snapshot` + `click getByRole button "Enviar"` funciona en una página
  real con varios botones.
- Pruebas unitarias del resolver.

## Verificación (2026-11-22, en vivo)
- En https://example.com: `snapshot` + `click getByRole link "Learn more"` navega a "Example Domains"; `get count` devuelve 1.
- En Studio: `click getByRole button "Crear"` abre el menú (aparece "Subir videos"); Escape lo cierra.
- Hit-test: con overlay cubriendo la página el click falla con error claro ("element is covered..."); sin overlay funciona.
- `go vet ./...` limpio, `go test ./...` en verde, unit tests nuevos en `internal/engine/targets_test.go`.
- Detalle de los cambios en `contexto/05-cierre-tareas.md`. Quedan como deuda-tecnica: targets en flags (--gone, --in), drag/upload con locator de varias palabras, hit-test en iframes.
