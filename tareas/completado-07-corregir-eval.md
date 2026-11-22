# Tarea 07 - Corregir eval para invocar la funcion (completado)

## Objetivo
Que `eval "() => ..."` ejecute la funcion como hace el JS original, en vez de devolver {} (la funcion serializada).

## Hallazgo
En la prueba real de Studio (2026-11-22): `eval "() => document.title"` devolvio {}. Engine.eval pasa la expresion tal cual a Runtime.evaluate con returnByValue, asi que una funcion flecha se serializa como objeto vacio. Workaround actual: envolver en IIFE.

## Criterio de aceptacion
- `eval "() => document.title"` devuelve el titulo como string.
- `eval "() => 1 + 1"` devuelve 2.
- `eval` con un target sigue recibiendo el elemento como argumento (semantica JS).
- `go test ./...` y `go vet ./...` limpios.

## Verificación (2026-11-22, en vivo en https://example.com)
- `eval "() => document.title"` devuelve `Example Domain`; `eval "() => 1 + 1"` devuelve `2`; `eval "document.title"` devuelve `Example Domain`.
- `eval "function(el){ return el.textContent; }" e1` devuelve `Learn more`; `eval "(el) => el.getAttribute('href')" e1` devuelve `https://iana.org/help/example-domains`.
- `go vet ./...` limpio, `go test ./...` en verde, unit tests nuevos en `internal/engine/eval_test.go`.
- Cambio extra encontrado en la prueba: `axNodeExpr` no casaba roles implícitos (`<a>` sin atributo role); ahora la consulta cubre atributo y etiqueta. Detalle en `contexto/05-cierre-tareas.md`.
