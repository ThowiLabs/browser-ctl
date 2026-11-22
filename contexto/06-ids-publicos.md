# Contexto 06 - IDs publicos estables de pestana (tarea 05, 2026-11-22)

## Diseno
El backend tenia `refs` y `nextSpare` declarados pero sin usar (el comentario
ya prometia el registro publico). Ahora `refs` es el registro: clave = ID
publico, valor = pestana (ID de Chrome + navegador). `refreshLocked` recorre
los navegadores en orden de conexion y asigna: si el ID de Chrome esta libre
como publico, se conserva; si ya esta tomado (por una pestana viva o por una
cerrada, cuyos refs nunca se borran), se asigna un spare desde 100001. Los
IDs publicos nunca se reutilizan en una ejecucion del daemon, asi que un ID
viejo nunca nombra otra pestana.

## Limites conocidos
- Que navegador conserva el ID de Chrome en una colision depende del orden
  en que el daemon descubre los navegadores (el relay los reporta como
  mapa, sin orden). Lo garantizado: los publicos son distintos y estables.
- Solo probado en vivo con un navegador (los IDs no cambian); la colision
  real entre dos navegadores solo esta cubierta por el test con extension
  falsa (`TestPublicTabIDsAcrossBrowsers`, 3/3 pases).
- `tab list` y `tab select/close/focus` trabajan con IDs publicos; el
  extension recibe siempre el ID de Chrome (closeTab, activateTab,
  groupTab, attach CDP).
