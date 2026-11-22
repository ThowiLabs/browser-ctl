# Tarea 01 — Probar la v2 en vivo en el Dell (en proceso)

## Objetivo
Copiar la v2 al Dell, compilarla con su Go y probarla contra Chrome real
con la extensión, verificando goto, tabs, snapshot y click.

## Pasos
1. Enviar el ZIP al Dell y reemplazar el código v1 (respaldo de v1 primero).
2. En el Dell: `go vet ./...`, `go test ./...`, `go build -trimpath -o browser-ctl.exe ./cmd/browser-ctl`.
3. Levantar el daemon y correr `goto https://example.com` dos veces:
   verificar que no se crea una segunda pestaña.
4. `snapshot`, `click` en un elemento real, `tabs`.
5. Probar el servidor MCP con un cliente MCP (initialize, tools/list,
   tools/call help y goto).

## Criterio de aceptación
- Compila y pasa vet/test en el Dell.
- Dos `goto` a la misma URL reusan la pestaña de la sesión.
- Snapshot devuelve refs y click actúa sobre el elemento.
- El daemon sobrevive a la salida del cliente MCP.

## Notas
- El MCP del Dell corre como SYSTEM: browser-ctl desde ahí queda con
  pairing pendiente. Para la prueba en vivo hace falta la terminal de Admin
  o confirmar el pairing desde Chrome.
