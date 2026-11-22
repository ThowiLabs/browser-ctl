# Contexto 04 - Prueba real en YouTube Studio (2026-11-22)

## Que se probo
Prueba real pedida por Yahir en su canal HirCoir (2,408 suscriptores): entrar a Studio, hablar con la IA "Preguntarle a Studio", pedirle datos, navegar secciones y enviar formularios. Todo con browser-ctl v2 desde el CLI, sin tocar la extension.

## Resultados (todo verificado con snapshot + get text + capturas)
- goto studio.youtube.com: cargo el Panel (titulo "Panel del canal - YouTube Studio"). La primera snapshot salio vacia porque la SPA aun cargaba; con wait 8000 ya salio el arbol completo.
- Click en "Preguntarle a Studio" (e13): abrio el panel de la IA.
- Click en la sugerencia "Resume el rendimiento de mi canal" (e84): la IA respondio con tabla real: 35,893 vistas (+1,196.7%), 414.8 h (+856.9%), 60,858 impresiones, CTR 4.59%, +76 suscriptores; top: "Microsoft rompio sus propios servidores | La caida de Outlook" 5,033 vistas.
- Formulario de la IA: el input es un DIV contenteditable con aria-label "Haz una pregunta" (no es textarea). Se enfoco con eval, se escribio "Cual fue mi video con mas vistas en los ultimos 28 dias?" con type y se envio con Enter. La IA respondio: el video de Outlook, 5,033 vistas, 83.4 h, 1:49 promedio, 76.05% retencion.
- Navegacion: click en "Analisis" (e14) llego a "Estadisticas del canal" con graficas (35.8 k vistas, 4,775 en 48 h). Click en "Contenido" (e17) la primera vez NO navego (devolvio exito pero la URL quedo en analytics); tras press Escape y reintentar con refs frescas si navego a "Contenido del canal" con la lista de videos.
- Formulario de busqueda: click en "Buscar en tu canal", type "Telcel", Enter: mostro el video "Se cayo Telcel: miles sin senal ni llamadas" con su descripcion.
- Capturas en C:\Users\Admin\Desktop\: studio-panel.jpg, studio-analytics.jpg, studio-contenido.jpg. Las tres se revisaron visualmente y son validas; la de Contenido muestra el panel de la IA con la conversacion completa.

## Hallazgos (bugs reales, no solo huecos)
1. eval NO invoca la funcion: `eval "() => document.title"` devuelve {} porque Runtime.evaluate recibe la funcion sin llamarla. El JS original la invoca. Workaround en la prueba: envolver en IIFE `(() => ...)()`. Tarea 07.
2. click con locator de varias palabras no pasa el parser: `click getByRole button "Nombre"` falla con "click takes at most 1 argument(s)". El target locator debe ser un solo argumento. Parte de la tarea 03.
3. Las refs se vuelven obsoletas cuando la pagina re-renderiza: click e471 fallo con "ref e471 isn't on the page anymore" tras responder la IA. Hay que re-sacar snapshot antes de clicar en paginas que cambian.
4. Click que devuelve exito sin efecto: el primer click en "Contenido" no navego (posible dropdown de busqueda interceptando el click). Sin hit-test no hay forma de detectarlo. Refuerza la tarea 03/05.
5. La snapshot inicial de una SPA pesada sale vacia si no se espera: Studio tardo mas de 3 s en montar el arbol AX.
