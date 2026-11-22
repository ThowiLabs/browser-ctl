# Tarea 05 - IDs publicos estables de pestana (completado)

## Objetivo
Que los IDs de pestana que ve el usuario sean estables entre navegadores: si el ID de Chrome choca entre dos navegadores, la pestana del segundo recibe un spare >= 100001, como hace el JS original. Hoy se usan los IDs de Chrome tal cual.

## Que se hizo (2026-11-22)
- `internal/backend/backend.go`: registro publico `refs` (public ID -> pestana, nunca se borra) + `nextSpare` desde 100001. `refreshLocked` asigna IDs estables: la pestana conserva su ID de Chrome salvo que ya sea un ID publico (de otra pestana, viva o cerrada); entonces recibe un spare. `resolveLocked` traduce ID publico -> (navegador, ID de Chrome) solo si la pestana sigue viva. Tabs/FindTab/TabClosed/CreateTab/CloseTab/ActivateTab/GroupTab/Claim trabajan con IDs publicos y traducen por dentro; el CDP siempre recibe el ID de Chrome. Un ID ya usado nunca nombra otra pestana.
- Test nuevo `TestPublicTabIDsAcrossBrowsers` en `internal/daemon/fake_test.go` (con `newFakeExtensionWith` para conectar dos extensiones falsas): dos navegadores con la misma tab 101 -> los publicos son {101, 100001}, estables entre lecturas, FindTab resuelve cada uno a su navegador, TabClosed distingue viva de desconocida. Pasa 3/3 veces seguidas.
- Limite conocido: que navegador conserva el 101 depende del orden de descubrimiento (el relay reporta navegadores como mapa); lo que se garantiza es que son distintos y estables. Ver contexto/06.

## Aceptacion verificada
- Unit: `go test ./...` en verde, `go vet ./...` limpio.
- En vivo con un solo navegador (Chrome real, daemon nuevo pid 3076): `tabs` muestra los mismos IDs de antes (1288343049, 1288343047, 1288343064), sin spares; `tab select 1288343064` + `get title` -> "The Internet" (la resolucion publico -> Chrome funciona).
- Multi-navegador en vivo no fue posible (solo hay un Chrome con la extension): cubierto por el test con extension falsa.
