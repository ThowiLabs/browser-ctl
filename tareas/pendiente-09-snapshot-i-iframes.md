# Tarea 09 - snapshot -i no muestra contenido de iframes (pendiente)

## Hallazgo
En `snapshot -i` el contenido de un iframe es invisible: ni el nodo Iframe ni el
párrafo/generic editable del editor TinyMCE aparecen, así que no hay refs
f<idx>e<n> para interactuar con ellos. El snapshot completo sí los muestra y
la interacción funciona (click/type por backend node id). Ver Hallazgo H en
contexto/07-verificacion-en-vivo.md y prueba 15 de la batería.

## Causa
El filtro de `snapshot -i` (`interactiveRoles` en internal/engine/snapshot.go)
solo conserva roles interactivos y main/navigation/banner/contentinfo. Ni
"Iframe" ni "paragraph" ni "generic" están ahí, así que el walk no les asigna
ref (y el contenido del iframe, que se camina anidado bajo el nodo Iframe,
tampoco recibe refs aunque el walk sí lo recorre).

## Criterio de aceptación
- `snapshot -i` en /iframe muestra al menos una ref que permita escribir en el
  editor (p. ej. el área editable como textbox, como hace el aria snapshot de
  Playwright con los contenteditable) y `click`/`type` con esa ref funcionan.
- `snapshot -i` en /nested_frames muestra refs para el contenido de los
  frames (al menos el texto MIDDLE alcanzable).
- Sin regresión: `go vet`/`go test` limpios, verificado en vivo, commit en
  español. Decidir con Yahir si el cambio de roles en -i es deseado antes de
  implementarlo (cambia lo que ven los usuarios de -i en todas las páginas).
