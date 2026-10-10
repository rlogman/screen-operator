# Guion del video (2 minutos)

Total: 120 s. Idioma: español. Frases cortas, ritmo parejo. La tarea en vivo se graba antes (ver
"Preparación") y se acelera si hace falta, mostrando el reloj.

Formato de cada bloque: línea `Tiempo: m:ss-m:ss (N s)`, qué se muestra y el texto a decir. Los
valores entre corchetes se completan con la medición final antes de grabar.

## Preparación (fuera del video)

- Escritorio de pruebas reseteado (`/__reset`) y el agente desplegado en la URL pública.
- Tarea en vivo: E1, crear cliente (instrucción literal de `material/pruebas/E1-crear-cliente.json`).
- Falla honesta: E5, homónimos (`material/pruebas/E5-homonimos.json`).
- Incidente: capturas antes y después de `docs/incidente-FR-0153.md` y su medición.
- No mostrar en pantalla contraseñas, tokens ni la URL privada del escritorio.

## Bloque 1 · Problema

Tiempo: 0:00-0:12 (12 s)

Pantalla: SIGECOM en el navegador, clics manuales sobre una tabla de clientes.

Texto:
"Una distribuidora usa un sistema de hace quince años. Sin API. Sin documentación.
Todo es clic a clic. Y tiene cinco sistemas así. Necesitamos un agente que los opere como un
empleado nuevo."

## Bloque 2 · Arquitectura

Tiempo: 0:12-0:30 (18 s)

Pantalla: diagrama C4 de contenedores (`docs/design/diagrams/c4-2-contenedores.png`), luego el de
ciclo de vida (`c4-4-dinamico.png`).

Texto:
"Es un servicio en Go con el contrato del evento. Recibe la tarea y el escritorio.
Se conecta por VNC, mira la pantalla y decide un paso a la vez. Los modelos van por OpenRouter,
sin herramientas de computer use. Las reglas de seguridad viven en código, no en el prompt."

## Bloque 3 · Tarea en vivo

Tiempo: 0:30-1:00 (30 s)

Pantalla: terminal con `POST /tasks` (token oculto), luego el escritorio operando solo: abre el
formulario, escribe, guarda, y el `GET /tasks/{id}` con `terminada` y el resumen.

Texto:
"Le pedimos crear un cliente nuevo. Mira la pantalla. Abre el formulario. Escribe cada campo.
Guarda. Y no se queda ahí: vuelve a leer la lista para verificar que el registro existe. Solo
entonces reporta terminada, con lo que cambió."

## Bloque 4 · Falla honesta: homónimos

Tiempo: 1:00-1:18 (18 s)

Pantalla: tarea E5 "Cambia el teléfono de Ana Torres", la lista con dos Ana Torres, y el resumen
`fallida` con los dos NIT.

Texto:
"Aquí hay dos clientes llamados Ana Torres. El agente no adivina. No cambia ninguno. Reporta fallida
y nombra los dos identificadores para que una persona decida. Preferimos una falla honesta a un
éxito dudoso."

## Bloque 5 · Respuesta al incidente

Tiempo: 1:18-1:42 (24 s)

Pantalla: captura antes y después de SIGECOM v4.2 (modal nuevo, botones movidos); línea de la traza
con el cambio de app detectado; diff del commit que toca solo `config/`.

Texto:
"A las tres y media el cliente cambió la app. Botones movidos, etiquetas nuevas, un modal al abrir.
Primero, el agente detecta el cambio: la huella de la pantalla no coincide con el perfil, y lo
registra. Segundo, se adapta: cierra el modal informativo y sigue sin las pistas viejas. Tercero, lo
explicamos: el perfil nuevo es solo configuración, y medimos antes y después."

## Bloque 6 · Métricas

Tiempo: 1:42-1:57 (15 s)

Pantalla: tabla "Resultados medidos" del README o `report.md` del harness.

Texto:
"Medimos con un harness propio. Oficiales correctos: [N de 5]. Corpus propio: [N por ciento].
Falsos éxitos: [N]. Costo medio por tarea: [N dólares], dentro del techo de cuatro centavos.
Tiempo p95: [N segundos]."

## Bloque 7 · Cierre

Tiempo: 1:57-2:00 (3 s)

Pantalla: URL del repositorio y del entrypoint público.

Texto:
"Screen Operator. Opera lo que no tiene API."
