# Tarea 07 - Corregir eval para invocar la funcion (pendiente)

## Objetivo
Que `eval "() => ..."` ejecute la funcion como hace el JS original, en vez de devolver {} (la funcion serializada).

## Hallazgo
En la prueba real de Studio (2026-11-22): `eval "() => document.title"` devolvio {}. Engine.eval pasa la expresion tal cual a Runtime.evaluate con returnByValue, asi que una funcion flecha se serializa como objeto vacio. Workaround actual: envolver en IIFE.

## Criterio de aceptacion
- `eval "() => document.title"` devuelve el titulo como string.
- `eval "() => 1 + 1"` devuelve 2.
- `eval` con un target sigue recibiendo el elemento como argumento (semantica JS).
- `go test ./...` y `go vet ./...` limpios.
