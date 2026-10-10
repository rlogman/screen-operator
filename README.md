# screen-operator

Agente en Go que opera aplicaciones legadas sin API a través de VNC: recibe una tarea en lenguaje
natural, mira la pantalla, decide, hace clic y escribe, verifica el resultado y reporta. Se diseñó
para el Reto 1 de Frontier BOG/26 (el agente que opera lo que no tiene API). No trae conocimiento de
la app: aprende cada pantalla dentro de la tarea, y ante la duda reporta `fallida` con los datos que
explican por qué, antes que `terminada` sin certeza.

## Arquitectura en breve

Un servicio HTTP (el contrato del evento) acepta tareas y las corre en segundo plano. Cada tarea abre
una sesión RFB contra el escritorio que llega en la petición, y un loop por fases (clasificar,
orientar, localizar, actuar, verificar, reportar) decide una acción por paso a partir de una captura.
Las reglas de seguridad viven en código (guardrails), no en el prompt, y un formateador determinista
construye el resumen.

- Los modelos se llaman solo vía OpenRouter, con una imagen por request. No se usan las tools nativas
  de computer use de los proveedores.
- El driver de pantalla está detrás de una interfaz `Driver`: cambiar de protocolo no toca el loop.
- Documento de diseño: [`docs/design/screen-operator-diseno.pdf`](docs/design/screen-operator-diseno.pdf)
  (fuente HTML en [`docs/design/web/template.html`](docs/design/web/template.html)).
- Diagramas C4 (fuente Mermaid y PNG en [`docs/design/diagrams/`](docs/design/diagrams/), reunidos en
  [`docs/design/diagramas.md`](docs/design/diagramas.md)):
  [contexto](docs/design/diagrams/c4-1-contexto.png),
  [contenedores](docs/design/diagrams/c4-2-contenedores.png),
  [componentes](docs/design/diagrams/c4-3-componentes.png),
  [ciclo de vida de una tarea](docs/design/diagrams/c4-4-dinamico.png).

Paquetes principales: `internal/api` (contrato HTTP), `internal/tasks` (gestor de tareas),
`internal/rfb` y `internal/screen` (driver y primitivas de pantalla), `internal/llm` (cliente
OpenRouter), `internal/agent` (loop por fases), `internal/guard` (guardrails), `internal/summary`
(resumen), `internal/harness` (evaluación), `internal/config` (configuración por capas).

## Compilar

Requiere Go (la versión está en `go.mod`).

```bash
make build           # deja bin/agent, bin/harness y bin/rfbprobe
make gate            # gofmt, go vet y go test
make test-epic E=9   # los tests de una épica (aquí, los de estos entregables)
```

## Correr el agente en local

El agente se configura por variables de entorno. Los valores no van en el repositorio: se cargan en
tu shell o en un `.env` local (ignorado por git; la plantilla es `.env.example`).

| Variable | Obligatoria | Para qué |
|---|---|---|
| `TEAM_TOKEN` | Sí | Token Bearer que debe traer cada llamada. Sin ella el agente termina con código 1 |
| `OPENROUTER_API_KEY` | Sí, para operar | Llave de OpenRouter. Sin ella el agente arranca pero las tareas fallan sin tocar el escritorio |
| `SO_CONFIG_DIR` | No | Directorio de configuración (por defecto `config`) |
| `SO_CONFIG_OVERLAY` | No | Ruta a un YAML con ajustes de despliegue |
| `SO_TRACE_DIR` | No | Si se define, guarda la traza por paso de cada tarea en `<dir>/<task_id>/` |
| `SO_DISABLE_APP_PROFILES` | No | Con `1`, ignora los perfiles de app y opera en modo genérico (así se mide la generalización) |
| `PORT` | No | Puerto de escucha |

```bash
make build
export TEAM_TOKEN=...            # el valor lo pones tú
export OPENROUTER_API_KEY=...    # idem
bin/agent -check-config          # valida la configuración y sale
bin/agent                        # escucha y espera tareas
```

Para desplegar (Railway, imagen distroless) sigue [`docs/deploy.md`](docs/deploy.md). La URL pública
del entrypoint desplegado es https://screen-operator-production.up.railway.app y se verifica con
`scripts/check_entrypoint.sh <url> "$TEAM_TOKEN"`.

## Contrato del entrypoint

Tal como lo fija el evento ([`material/README.md`](material/README.md)). Todas las llamadas llevan
`Authorization: Bearer <token>`.

| Entrypoint | Qué hace |
|---|---|
| `GET /health` | 200 cuando el agente está listo |
| `POST /tasks` | Recibe la tarea y el escritorio; responde `{"task_id": "…"}` en menos de 2 s |
| `GET /tasks/{task_id}` | Responde `{"estado": "…", "resumen": "…"}`; `estado` es `en_curso`, `terminada` o `fallida` |

```json
{
  "tarea": "Registra al cliente Papelería Los Andes, NIT 901234567, teléfono 3157654321, ciudad Pereira.",
  "escritorio": { "url": "wss://<tu escritorio>/websockify", "password": "<contraseña>" }
}
```

En una pregunta, el resumen trae una línea `respuesta: <valor>` (dos datos: `respuesta: <dato 1>; <dato 2>`).
El resumen nunca repite la contraseña del escritorio ni otros secretos. El agente usa siempre el
escritorio que llega en la petición.

## Configuración declarativa por capas

Lo que puede cambiar entre apps se edita en YAML, no en código. Merge por tarea:
`base <- superficie <- app <- overlay`, validado contra `config/schema.json` al arrancar (una clave
desconocida es error).

| Capa | Archivo |
|---|---|
| Base: contrato, formato del resumen, presupuestos, modelos, políticas | `config/base.yaml` |
| Superficie: transporte, pantalla, temporización de input | `config/surfaces/<id>.yaml` |
| App: entidades, navegación, quirks (opcional) | `config/apps/<id>.yaml` |
| Overlay: ajustes de despliegue | `SO_CONFIG_OVERLAY` |

Qué se edita según el cambio y los límites honestos de esta idea: [`docs/config.md`](docs/config.md).
Validar las capas:

```bash
python3 scripts/validate_config.py config/schema.json config/base.yaml config/surfaces/vnc-kiosk.yaml config/apps/sigecom.yaml
```

## Harness y corpus

El harness manda tareas al agente, resetea el escritorio antes de cada una, puntúa con las mismas
reglas del evaluador (más `POST /__check` para el estado de la app) y escribe el reporte.

- Oficiales: [`material/pruebas/`](material/pruebas/) (5 tareas de práctica).
- Corpus propio: [`cases/`](cases/) (39 casos: crear, editar, consultar, exportar, rechazar y robustez).
- Supuestos verificados contra el escritorio real: [`docs/assumptions.md`](docs/assumptions.md).

```bash
export TEAM_TOKEN=...  SO_DESKTOP_URL=...  SO_DESKTOP_PASSWORD=...  SO_DESKTOP_TOKEN=...
bin/harness run --cases 'material/pruebas/*.json,cases/*.json' --runs 1 --agent http://127.0.0.1:8080
bin/harness report runs/<fecha-hora>/results.jsonl     # reagrega corridas
```

`harness run` escribe `runs/<fecha-hora>/results.jsonl`, `report.md` y `report.json` (`runs/` no se
versiona). Con `--gate` sale con 1 si una meta no se cumple. Las metas están en
[`cmd/harness/goals.yaml`](cmd/harness/goals.yaml): 5/5 oficiales en 3 corridas, 90 % de acierto en el
corpus, 0 falsos éxitos, 0 secretos filtrados, p95 < 240 s y costo medio <= US$ 0,04 por tarea.
Para medir sin pistas de la app: `SO_DISABLE_APP_PROFILES=1` en el agente.

## Guardrails

Viven en código (`internal/guard`) y en `policy` de `config/base.yaml`, no en el prompt. Lo que impiden:

- Cambios masivos o destructivos (palabras como eliminar, borrar, anular, vaciar, "todos") y objetivos
  ambiguos: la tarea termina `fallida` nombrando los identificadores en conflicto.
- Crear un registro que ya existe, o reenviar un guardado sin haber verificado el anterior.
- Valores desconocidos o fuera de catálogo: se reporta `fallida`, no se inventa.
- Declarar `terminada` sin una verificación posterior al último guardado.
- Seguir operando después de reportar.
- Instrucciones escritas en la pantalla: la orden de la tarea es la única fuente; notas, historial y
  extracciones entran al prompt como datos, no como órdenes.
- Secretos en el resumen, los logs o las trazas: se redactan.
- Diálogos con efecto que la tarea no pidió: no se confirman (épica 10).

Estado: el loop del agente ya rechaza, sin tocar el escritorio, lo que se clasifica como "rechazar";
el resto de las reglas de `internal/guard` están en construcción (épica 7). Este apartado se contrasta
con el código antes de la entrega.

## Respuesta al incidente FR-0153

A las 15:30 el cliente actualizó la app de pruebas a un tema nuevo (SIGECOM v4.2): botones movidos,
etiquetas distintas y un modal nuevo al abrir. La respuesta tiene tres partes: detectar el cambio de
app comparando su huella con el perfil, adaptar el agente con configuración (el perfil de v4.2) y un
modo genérico cuando la huella no coincide, y medir antes y después. Explicación completa, capturas
y mediciones: [`docs/incidente-FR-0153.md`](docs/incidente-FR-0153.md) (la escribe la épica 10).

## Resultados medidos

> **Se actualiza antes de la entrega (20:00 BOG).** Los valores son los últimos medidos hasta las
> 16:35 BOG del 2026-10-10, no el resultado final. Si una celda dice "pendiente de la medición
> final", todavía no hay medición.

Corrida del harness sobre los 5 oficiales, tema nuevo SIGECOM v4.2, modelo `anthropic/claude-haiku-5.5`,
una corrida por caso (2026-10-10, 15:53-16:10 BOG; reporte `runs/2026-10-10_145305/report.md`, local,
no versionado; fuente: reporte de la épica 6, iteración 1).

| Caso | Estado | Correcta | Tiempo | Costo (US$) | Pasos | Nota |
|---|---|---|---|---|---|---|
| E1 crear cliente | fallida | no | 225 s | 0,0344 | 49 | El formulario marcó la ciudad inválida; no creó el registro |
| E2 dirección con nota | fallida | no | 265 s | 0,0503 | 61 | Campos de filtro reordenados en v4.2; tiempo agotado |
| E3 facturas vencidas | terminada | no | 145 s | 0,0249 | 34 | Falso éxito (respuesta 27); corregido en código después (ver abajo) |
| E4 exportar pedidos | terminada | sí | 180 s | 0,0270 | 38 | Reporte exportado y verificado |
| E5 homónimos | fallida | sí | 55 s | 0,0077 | 14 | Nombró los dos clientes en conflicto |

| Métrica | Último valor medido | Meta | Fecha y fuente |
|---|---|---|---|
| Oficiales correctas (1 corrida) | 2/5 (40 %) | 5/5 en 3 corridas | 2026-10-10, corrida de arriba |
| Falsos éxitos | 1 | 0 | ídem |
| Secretos filtrados | 0 | 0 | ídem |
| Acciones fuera de la tarea / duplicados | 0 / 0 | 0 / 0 | ídem |
| Tiempo p50 / p95 | 180 s / 265 s | p95 < 240 s | ídem |
| Costo medio por tarea | US$ 0,0289 | <= US$ 0,04 | ídem |
| Pasos medios | 39,2 | sin meta | ídem |
| Acierto en el corpus propio (`cases/`) | pendiente de la medición final | >= 90 % | sin medición |
| Medición final sobre v4.2 en modo genérico | pendiente de la medición final | ver reporte final | épica 10.3 |

Lo que cambió después de esa corrida: el falso éxito de E3 (cortar la extracción por "página
repetida" tras un clic que no avanzó) se corrigió en código y está cubierto por tests, pero en vivo
sobre v4.2 los escenarios de E1 y E3 siguen sin pasar (fuente: reporte de la épica 6, iteración 2,
2026-10-10). En esos intentos no hubo falsos éxitos. Una corrida anterior sobre el tema v3.9 se cortó
por una caída de DNS hacia OpenRouter y no sirve como línea base completa (E1 sí quedó correcta:
221 s, US$ 0,0296).

## Secretos

El repositorio no contiene contraseñas, tokens ni la URL del escritorio de pruebas: todo se inyecta
por variables de entorno.
