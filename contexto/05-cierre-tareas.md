# Contexto 05 - Cierre de tareas pendientes (2026-11-22)

## Tarea 02 - Instalación (completado, commit c87b56f)
Verificado el estado instalado: commit c83ae96 presente en master, `go vet` limpio, `go test` en verde (daemon ok 1.127s, engine ok 0.020s), binario `browser-ctl.exe` (12,535,296 bytes) y daemon en 127.0.0.1:9010 (pid 4632, version 2.0.0). La tarea solo estaba en-proceso en nombre; el trabajo ya estaba hecho.

## Tarea 07 - eval no invoca la función (completado)
- Causa: `Engine.eval` pasaba la expresión tal cual a Runtime.evaluate, así que una función flecha se serializaba como `{}`.
- Cambio (`internal/engine/reads.go`): `doEval` ahora envuelve la expresión en `wrapEvalExpr`: si evalúa a función, la invoca con el elemento objetivo como argumento (o `undefined` sin target); un valor plano pasa tal cual. El target opcional (2º argumento) se resuelve con la nueva `ResolveTargetSess` (`internal/engine/targets.go`), que usa la tabla de refs del snapshot para las refs. Además `formatEvalValue` imprime strings sin comillas JSON (null se distingue por tipo: unmarshalar null a string no da error).
- Hallazgo durante la prueba: `axNodeExpr` (`internal/engine/snapshot.go`) buscaba solo `[role="link"]`, que no casa con `<a>` (los roles implícitos no son atributo). Ahora la consulta cubre atributo de rol y etiqueta implícita (`[role="link"],a`) usando el `roleTag` que ya existía. Esto arregla también la resolución de refs para fill/check/select/get con elementos de rol implícito.
- Pruebas unitarias nuevas: `internal/engine/eval_test.go` (TestWrapEvalExpr, TestFormatEvalValue).
- Verificación en vivo en https://example.com (con daemon reiniciado tras el rebuild, porque el daemon viejo seguía sirviendo el binario anterior): `eval "() => document.title"` devuelve `Example Domain`; `eval "() => 1 + 1"` devuelve `2`; `eval "document.title"` devuelve `Example Domain`; `eval "function(el){ return el.textContent; }" e1` devuelve `Learn more`; `eval "(el) => el.getAttribute('href')" e1` devuelve `https://iana.org/help/example-domains`.
- Lección: tras cada rebuild hay que reiniciar el daemon (`browser-ctl.exe shutdown`) o la prueba en vivo corre contra el binario viejo.
