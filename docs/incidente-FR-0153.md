# Incidente FR-0153: la app de pruebas cambió de tema

15:30 BOG, 2026-10-10. El cliente actualizó SIGECOM de v3.9 a v4.2 sin avisar: colores nuevos,
botones con otro nombre y en otro lugar, filtros reordenados y un modal nuevo al abrir la app.
El set oculto corre sobre v4.2. Este documento explica qué cambió, cómo lo detecta el agente,
qué se resolvió con configuración y qué con código, y cuánto cambió la medición.

## 1. Qué cambió (antes y después)

| | v3.9 (antes) | v4.2 (después) |
|---|---|---|
| Inicio | ![v3.9 inicio](incidente/v3.9-inicio.png) | ![v4.2 modal](incidente/v4.2-modal-novedades.png) |
| Encabezado de Facturas | ![v3.9](incidente/v3.9-facturas-encabezado.png) | ![v4.2](incidente/v4.2-facturas-encabezado.png) |

Más capturas de v4.2, todas tomadas con `rfbprobe` sobre el escritorio recién reseteado:
[inicio sin el modal](incidente/v4.2-inicio.png), [Clientes](incidente/v4.2-clientes.png),
[Crear cliente](incidente/v4.2-crear-cliente.png), [Facturas](incidente/v4.2-facturas.png).

| Aspecto | v3.9 | v4.2 | Impacto en el agente |
|---|---|---|---|
| Al abrir | pantalla de inicio | modal "Novedades de la versión 4.2" con "Entendido" y "x"; velo gris sobre la app | bloquea todo clic hasta cerrarlo |
| Barra de título | azul, "v3.9" | verde oscuro, "v4.2" | la huella de la app cambia |
| Menús | Archivo, Comercial, Cartera, Herramientas, Ayuda | iguales, mismas posiciones | ninguno |
| Filtros de Clientes | otro orden | Nombre, NIT, Ciudad (desplegable), **Filtrar**, Limpiar | en la corrida previa el agente escribió el nombre en NIT |
| Crear y exportar | a la izquierda | **Crear cliente** / **Crear pedido** y **Exportar** a la derecha, sobre la tabla | etiquetas y posiciones nuevas |
| Formularios | Guardar (cierra), Volver | **Aplicar** (guarda y el formulario queda abierto), **Cerrar** | la verificación ya no puede esperar que el formulario se cierre |
| Paginación | igual | "Página 1" sin total; Anterior / Siguiente | igual |

Los datos (clientes, pedidos, facturas) no cambiaron.

## 2. Cómo lo detecta el agente

Antes de tocar la aplicación, el agente hace una **revisión de apertura**
(`internal/agent/opening.go`, prompt `internal/agent/prompts/opening_v1.tmpl`): con la captura
inicial, el modelo devuelve el título de la app, la **versión visible** y, si lo hay, el diálogo
que bloquea la pantalla (título, tipo y botones con coordenadas). Dos cosas salen de ahí:

1. **Diálogos (10.1).** El código, no el modelo, decide qué hacer con `policy.dialogs`
   (`config/base.yaml`), y el modelo solo puede subir el riesgo, nunca bajarlo. Un diálogo es con
   efecto si el modelo no lo lee informativo, si el título o un botón tienen una palabra de efecto
   (eliminar, confirmar, pagar, aplicar, guardar, cambios...), si el título o el texto tienen un
   verbo de `policy.destructive.keywords` ("Se eliminará el registro"), si ofrece Cancelar / No /
   Volver junto a otro botón, o si un botón repite el verbo del diálogo ("¿Cerrar sesión?"
   [Cerrar]). Uno informativo se cierra con un botón de cierre (Entendido, Aceptar, Cerrar...);
   uno con efecto **nunca se confirma**: se cancela con Cancelar / No / Volver, si no Cerrar, si no
   la x, y nunca con un botón que repite el verbo del diálogo (en "¿Cerrar caja?" [Cerrar,
   Cancelar] se elige Cancelar). Si no hay opción segura, la tarea termina `fallida` nombrando el
   diálogo sin tocar el escritorio. Después
   del clic, otra revisión confirma que la pantalla principal quedó libre. La traza registra
   `diálogo informativo cerrado: "Novedades de la versión 4.2" (boton "Entendido")`.
2. **Huella de app (10.2).** Título y versión se comparan con el perfil (`app.match.screen_text_any`
   y el campo nuevo `app.match.version`). Si el texto coincide pero la versión no, las pistas del
   perfil **no se usan**, la traza y el log registran `cambio de app detectado: v3.9 -> v4.2` y la
   tarea sigue en modo genérico, aprendiendo la app desde la pantalla. Así, un perfil viejo nunca
   le dice al modelo "pulsa Guardar" sobre una app que ya no tiene ese botón.

Evidencia en vivo (E10.1-S3, `TestE10_1_S3_LiveOpeningClosesNewModal`, escritorio recién reseteado):

```
traza: cerrar dialogo: click(840,483) "Entendido" | diálogo informativo cerrado: "Novedades de la versión 4.2" (boton "Entendido")
traza: revisar apertura: SIGECOM · Distribuidora Andina | la pantalla principal quedo libre de dialogos
huella="SIGECOM · Distribuidora Andina\nversion v4.2" llamadas=2 tokens=9251/291 costo=US$0.0011
```

## 3. Qué fue configuración y qué fue código

**Solo configuración** (commit `75a5e55`, toca únicamente `config/`):
- `config/apps/sigecom.yaml`: `app.match.version: v4.2` y las etiquetas nuevas relevadas con
  `rfbprobe` (modal Novedades/Entendido, orden de filtros, Filtrar, Crear cliente/pedido a la
  derecha, Aplicar guarda sin cerrar, Cerrar sale, paginación sin total).
- `config/schema.json`: `app.match.version`, `app.match.version_pattern` y `policy.dialogs`.

Un cambio de tema futuro de una app conocida se resuelve igual: relevar con `rfbprobe`, editar el
perfil y subir `app.match.version`. Sin tocar código.

**Código** (genérico, no nombra SIGECOM ni coordenadas):
- `internal/agent/opening.go` + `prompts/opening_v1.tmpl`: revisión de apertura, decisión de
  diálogos en código, paso por `Guard.BeforeAction`.
- `internal/agent/opening.go` (commit 8c4bfc8): un diálogo informativo cuyo cierre se confirmó al
  abrir queda recordado (región alrededor del botón); si reaparece idéntico durante la tarea se
  cierra en código, sin paso ni llamada al modelo (máximo 8 veces).
- `internal/config/fingerprint.go`: `ResolveAppFingerprint` y `VisibleVersion`.
- `internal/agent/agent.go` (parte de orientar) y `runner.go`: conectan la revisión y la huella.
- `config/base.yaml`: `policy.dialogs` (etiquetas de cierre, de cancelación y palabras de efecto,
  editables sin código).

**Tests que dependían del tema:**
- `TestE2_6_S5` (Cartera > Facturas): cierra antes el modal (`SO_E2_6_DISMISS_XY`, default el
  botón Entendido de v4.2) y su referencia de encabezado se regrabó sobre v4.2 (mismo lugar,
  colores nuevos). `TestE10_3_S1` verifica en vivo que ese default cierra el modal.
- Corpus (`cases/`): ninguna instrucción ni clave de razón nombra etiquetas de la interfaz
  (`TestE10_3_S3_CorpusDoesNotDependOnTheme`). Las evidencias `cases/evidence/*.png` son capturas
  **de v3.9** y quedan marcadas como tales aquí; los datos que muestran no cambiaron en v4.2.

## 4. Medición antes y después

Todas las corridas: harness con el agente local, `SO_DISABLE_APP_PROFILES=1` (modo genérico,
sin pistas de perfil), una corrida por caso, horas locales del equipo (BOG-1).

**Antes** (reporte de la épica 6, sin la revisión de apertura):

| Corrida | Tema | Acierto oficiales | Falsos éxitos | Nota |
|---|---|---|---|---|
| épica 6, corrida 1 | v3.9 | 1/5 | 0 | E3-E5 cortados por una caída de DNS hacia OpenRouter |
| épica 6, corrida 2 | v4.2 | 2/5 | 1 (E3 "respuesta: 27") | modal cerrado por el modelo a ojo; nombre escrito en el filtro NIT |

**Después** (esta épica, sobre v4.2):

| Corrida | Código | Casos | Acierto | Falsos éxitos | p95 | Costo medio | Nota |
|---|---|---|---|---|---|---|---|
| `runs/2026-10-10_153607` | 62be776 (antes de las iteraciones 2-3 de la épica 6) | 5 oficiales | 1/5 | 1 (E2: clic en Exportar en Clientes) | 265 s | US$ 0,032 | **dudosa**: se solapó con la medición de la épica 7 en el mismo escritorio |
| `runs/2026-10-10_155638` | 5d279fd (merge de la épica 6 it. 2-3) | 5 oficiales | 3/5 (E3, E4, E5) | 0 | 265 s | US$ 0,026 | **dudosa**: el lock mostró otro dueño a las 16:05 mientras corría |
| `runs/2026-10-10_161225` | 5d279fd | 6 del corpus (P01, P06, P13, P23, P29, P35) | 2/6 (P29, P35) | 0 | 265 s | US$ 0,024 | 16:12-16:30; **solape no verificado** (hay una corrida de la épica 7 con marca 16:12:24, `.claude/worktrees/agent-a4a2ee55b429748ba/runs/e7/2026-10-10_161224`, no versionada) |
| `runs/2026-10-10_163011` | 8c4bfc8 (final: también cierra el modal si reaparece) | 5 oficiales | 2/5 (E4, E5) | 0 | 265 s | US$ 0,020 | 16:30-16:43; **solape no verificado** (el reporte de esa corrida de la épica 7 tiene hora 16:34; no se revisó el lock) |

Reporte agregado de la medición final (oficiales con el código final + corpus):
[`runs/2026-10-10_164316/report.md`](../runs/2026-10-10_164316/report.md) (no versionado; `runs/`
está en `.gitignore`), copia versionada en
[`incidente/medicion-v4.2/report.md`](incidente/medicion-v4.2/report.md): 4/11 (36,4 %),
**0 falsos éxitos**, 0 secretos, 0 fuera de la tarea, 0 duplicados, p50 155 s, p95 265 s,
costo medio US$ 0,022.

Lectura honesta:
- Lo que el incidente rompió quedó resuelto: el modal se cierra en código en todas las tareas
  (traza `diálogo informativo cerrado` en 20 de las 21 tareas medidas; P29 se rechazó antes de tocar la app) y
  ya no consume pasos del modelo; el perfil viejo no puede inyectar etiquetas de v3.9.
  Fuente del "20 de 21": los logs del agente de las cuatro corridas, **no versionados** (`runs/`
  está en `.gitignore`) y que solo existen en el worktree de la medición:
  `.claude/worktrees/agent-a38295261988781fc/runs/e10-{t1,c2,c3,c11}/agent.log` (153607, 155638,
  161225 y 163011). Conteo reproducible: por log, tareas = líneas `"tarea recibida"` y tareas con
  cierre = `task_id` distintos en líneas con `diálogo informativo cerrado` (5/5, 5/5, 5/6, 5/5).
- Los falsos éxitos bajaron de 1 a 0 en las corridas con el código de la épica 6 it. 2-3.
- El acierto no mejoró de forma clara (2/5 a 3/5 a 2/5 entre corridas): la causa dominante de
  los fallos es tiempo agotado (265 s) navegando menús y filtros con el modelo barato, no el tema.
  Con una corrida por caso la varianza es alta.
- El corpus quedó medido parcialmente: 6 de 39 casos (uno por categoría), por tiempo de
  escritorio compartido antes del congelamiento de código. Los 33 restantes no se corrieron.
- Hallazgo de la medición: el modal de novedades **reaparece a mitad de tarea** (el modelo lo
  cerró hasta 6 veces en una tarea de la corrida `155638`). El commit 8c4bfc8 lo cierra en
  código cuando reaparece idéntico; en la corrida final no reapareció, así que ese camino solo
  está cubierto por el test en memoria `TestE10_1_S1_ReappearingDialogClosedInCode`.
