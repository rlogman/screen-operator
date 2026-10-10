# Contrato de delegación: screen-operator-v1

Fecha: 2026-10-10 (Frontier BOG/26, Reto 1). Horas en Bogotá (UTC-5).

## Scope

Épicas 1 a 9 de `.artifex/tickets.db` (fuente: `docs/requirements.json`), 39 historias, 161 escenarios Gherkin. Estado al escribir este contrato: 39 TO_DO, 0 IN_PROGRESS, 0 DONE (re-derivar con `ticket-manager.sh status` al iniciar cada épica).

| Épica | Historias | Orden |
|---|---|---|
| 1 Fundaciones y configuración | 1.1-1.3 | 1 |
| 2 Driver RFB y primitivas de pantalla | 2.1-2.7 | 2 (puede ir en paralelo con 3) |
| 3 API del contrato y gestión de tareas | 3.1-3.5 | 2 (puede ir en paralelo con 2) |
| 4 Despliegue temprano | 4.1-4.2 | 3 |
| 5 Harness de evaluación y corpus | 5.1-5.6 | 4 |
| 6 Núcleo del agente | 6.1-6.6 | 5 |
| 7 Guardrails y verificación | 7.1-7.5 | 6 |
| 8 Optimización por datos | 8.1-8.3 | 7 |
| 9 Entregables finales | 9.1-9.2 | 8 |

## Out of scope

- **El cambio de requisitos anunciado para las 15:30.** No se conoce. Cuando llegue, el orquestador pausa en el siguiente límite de iteración registrado, el humano decide, y se crean tickets nuevos. No se anticipa en código.
- **Ejecutar el despliegue en Railway y grabar el video.** Usan cuentas del equipo. El loop deja readiness (imagen, runbook, script de verificación, guion). Ejecutarlos requiere el OK explícito del usuario.
- **Disparar el set oculto del evento** (3 corridas en total). Nunca lo dispara el loop.

## Read first

Leer completos antes de empezar:
- `material/README.md` (contrato y reglas de evaluación) y `material/pruebas/*.json`
- `docs/design/web/template.html` (diseño; publicado en https://claude.ai/artifact/XzG4LXQzwR273Y6dbywrGL)
- `docs/config.md`, `config/*.yaml`, `config/schema.json`
- `docs/requirements.json` (convenciones al inicio del archivo)
- Este contrato y `screen-operator-v1.decisions.md`

## Closing rule

Cada historia termina DONE o SHELVED con un disparador documentado. Ninguna queda sin cerrar en silencio. Un criterio que despliega, envía o ejecuta algo con efecto real se cumple demostrando readiness (build reproducible, gates verdes, runbook), no ejecutándolo.

El loop externo cierra cuando las 39 historias están DONE o SHELVED con disparador y el gate global pasa.

## Mechanism

Dos niveles de `/afx-loop`:

1. **Loop externo `so-v1`**: goal = todo el scope; criterio de salida = 39 historias cerradas + gate global verde. No ejecuta código: encadena los loops de épica y registra su resultado.
2. **Loops internos `so-epic-<n>`**, uno por épica, `--max=3`:
   - **Executor** (fresco por iteración, worktree aislado): implementa TODAS las historias de la épica en orden de `implementation_order`. Por historia solo exige L1 (`go build ./... && go vet ./...`), marca IN_PROGRESS al empezar y escribe los tests de sus escenarios con la convención `TestE<n>_<m>_S<k>_*`. **No corre la suite por historia.** Al terminar la épica corre el gate de épica una vez y reporta.
   - **Validator** (agente distinto): corre el gate de épica, revisa el packet contra los escenarios de la épica, y da PASS/FAIL con el panel adversarial del perfil `prototype` (cliente insatisfecho = criterios; atacante activo porque se manejan credenciales reales).
   - En PASS: merge del branch a `main`, `ticket-manager.sh done` para cada historia de la épica, `sync` del JSONL, commit. En FAIL: el feedback va al siguiente executor.
   - Épicas 2 y 3 pueden correr en paralelo (worktrees distintos) porque no comparten paquetes.

## Gates

| Nivel | Cuándo | Comando |
|---|---|---|
| L1 | Por historia | `go build ./... && go vet ./...` |
| Gate de épica | Una vez por épica | `make gate` + `make test-epic E=<n>` + `python3 scripts/gherkin_coverage.py <n>` |
| Escenarios reales | En el gate de las épicas 2, 5, 6, 8 | `set -a; . ./.env; set +a; go test -tags desktop,live ./... -run '^TestE<n>_' -count=1` |
| Gate global | Cierre del loop externo | `make gate` + `python3 scripts/gherkin_coverage.py` + `scripts/validate_config.py` sobre las capas + harness sobre los 5 oficiales |

Si los escenarios reales fallan porque falta `.env` o el escritorio no responde, se clasifican como ENVIRONMENTAL (no CODE) y no consumen iteración; se reportan al humano.

## Autonomy

Autoridad completa sobre el CÓMO. Dos límites duros:
1. Nunca reducir el QUÉ. Ningún criterio se recorta, suaviza o reinterpreta para pasar. Uno imposible, incorrecto o inseguro se detiene y se reporta como bloqueante.
2. Cada decisión autónoma de CÓMO se registra al tomarla en `.artifex/delegations/screen-operator-v1.decisions.md` y se resume completa al cerrar.

## Decision criterion

Favorecer un producto sólido que el cliente opere solo, sin escalar lo rutinario. Para este reto: ante duda, `fallida` honesta antes que `terminada` dudosa; costo por tarea dentro de US$ 0,04; nada secreto fuera.

## Stack constraints

De `~/.claude/CLAUDE.md`: "Go preferred. Rust for native applications. Python only for quick scripts." "Do not guess APIs, versions, flags, commit SHAs, package names, or behavior. Verify by reading code, Context7 MCP, or the web."

Del diseño: driver detrás de interfaz `Driver`; las tools nativas de computer use de los proveedores no son el default; modelos solo vía OpenRouter; las reglas de seguridad viven en código, no en el prompt.

## Credenciales

`.env` (ignorado por git) lo completa el usuario a partir de `.env.example`. Ningún agente imprime, registra ni commitea sus valores. Ningún test escribe valores reales en `testdata/`.

## Tiempo

Cierre duro de ejecución: 20:00. Si a las 18:30 queda una épica sin cerrar, sus historias pendientes se SHELVEAN con el disparador "sin tiempo antes del cierre de las 20:00" y el loop externo cierra con el estado real, sin marcar nada como DONE que no lo esté.

## Ampliación de alcance (16:00 BOG, aprobada por el usuario)

Épica 10 "Incidente FR-0153: la app cambió de tema" (historias 10.1 a 10.4) entra al alcance. Se ejecuta en paralelo con la épica 7 apenas la épica 6 entre a main. Por tiempo, la épica 8 es la primera candidata a SHELVED con el disparador "sin tiempo antes del cierre de las 20:00".
