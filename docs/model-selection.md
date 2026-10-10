# Selección de modelos (historia 8.2)

Medido el 2026-10-10 entre las 17:05 y las 17:49 BOG sobre SIGECOM v4.2 (tema nuevo del incidente FR-0153), con `SO_DISABLE_APP_PROFILES=1`, una corrida por caso y el harness de la épica 5 (`bin/harness run`). Precios e IDs verificados en `https://openrouter.ai/api/v1/models` el mismo día:

| Modelo | Entrada US$/M | Salida US$/M | Tokens de entrada por captura 1280x800 (TestE6_1_S5) | US$ por captura |
|---|---|---|---|---|
| `anthropic/claude-haiku-5.5` | 0,10 | 0,50 | 1.412 | 0,00019 |
| `anthropic/claude-sonnet-5.5` | 2,00 | 10,00 | 1.412 | 0,0036 |
| `google/gemini-3.8-flash` | 0,75 | 3,75 | 1.131 | 0,0012 |

Gemini 3.8 Flash y Sonnet 5.5 rechazan `reasoning_effort: none` (HTTP 400, "Reasoning is mandatory for this endpoint and cannot be disabled"). Con `minimal`, ambos reportan 0 tokens de razonamiento.

Techo de costo medio: US$ 0,07 por tarea. El set oculto tiene 56 tareas y la reserva es de US$ 5, lo que da ~0,089 por tarea; el 0,07 deja margen para las tareas difíciles. Lo fijó el orquestador y reemplaza el 0,04 del brief.

## Tabla

Subconjunto común: E1, E2 y E3, los tres oficiales más informativos (crear con desplegable, filtrar y editar, contar con paginación). Lo autorizó el orquestador por falta de tiempo de escritorio. p95 por rango más cercano: con 3 casos es el máximo.

| Combinación | Actor | Escalamiento | Acierto | Falsos éxitos | p95 (s) | Costo medio (US$) | Pasos medios |
|---|---|---|---|---|---|---|---|
| A | `anthropic/claude-haiku-5.5` | - | 0/3 | 1 | 265 | 0.0365 | 48.0 |
| B | `anthropic/claude-haiku-5.5` | `anthropic/claude-sonnet-5.5` | 2/3 | 0 | 115 | 0.0439 | 25.0 |
| C | `google/gemini-3.8-flash` | - | 0/3 | 0 | 265 | 0.1452 | 50.3 |

Por caso:

| Caso | A | B | C |
|---|---|---|---|
| E1 crear cliente | fallida, 225 s, 49 pasos, US$ 0,0344 | fallida honesta, 95 s, 22 pasos, US$ 0,0335 (guardó pero no pudo verificar) | fallida, 265 s (tiempo), 49 pasos, US$ 0,1380 |
| E2 dirección con nota | fallida, 265 s, 61 pasos, US$ 0,0503 | **correcta**, 105 s, 25 pasos, US$ 0,0355 | fallida, 265 s, 51 pasos, US$ 0,1543 |
| E3 facturas vencidas | falso éxito, 145 s, 34 pasos, US$ 0,0249 | **correcta**, 115 s, 28 pasos, US$ 0,0626 | fallida, 265 s, 51 pasos, US$ 0,1432 |

En B se escalaron 8 decisiones en 3 tareas: 2, 2 y 4. Siete fueron por `sin_avance` y una por `sin_efecto`.

En los 5 oficiales, C acertó 1/5 (solo E5, una fallida esperada), con 0 falsos éxitos, p95 265 s, costo medio US$ 0,1196 y 42,2 pasos medios. A acertó 2/5, con 1 falso éxito, p95 265 s, US$ 0,0289 y 39,2 pasos.

Fuentes:
- A: `runs/2026-10-10_145305` del worktree de la épica 6 (15:53 BOG, v4.2, mismos 5 oficiales). **No se volvió a medir** con el código actual de main por falta de tiempo de escritorio. Ese código es anterior a la iteración 3 de la épica 6, que cerró los caminos de falso éxito en conteos. Con el código actual, el falso éxito de E3 probablemente sería una fallida honesta (inferido, no verificado). El acierto de A en E1-E3 seguiría siendo 0/3.
- B: `runs/B-haiku-sonnet/2026-10-10_164400` (17:44-17:49 BOG), con el lock del escritorio y sin otra corrida activa.
- C: `runs/C-gemini/2026-10-10_160510` (17:05-17:24 BOG). Solo E1 corrió con el escritorio en exclusiva. Desde las 17:12 corrieron en paralelo las mediciones de las épicas 7 y 10, porque el lock se perdió (ver EXECUTOR_REPORT). E2 y E3 de C están contaminados. Aun así, el costo descarta a C sin depender del acierto: cada paso de Gemini costó ~US$ 0,0028, contra ~US$ 0,0007 de Haiku, y las tareas largas superan US$ 0,13.

## Elección

Elegida: **B**

Regla (E8.2-S2): entre las combinaciones con 0 falsos éxitos y costo medio <= techo, la de mayor acierto y, a igual acierto, la de menor costo. Si ninguna mejora el acierto de A, se queda A. `TestE8_2_S2_ChoiceFollowsRule` aplica la regla sobre esta tabla y `TestE8_2_S3_BaseYamlReflectsChoice` verifica que `config/base.yaml` la refleje.

## Razón

Elegida B: Haiku 5.5 como actor y Sonnet 5.5 como escalamiento. Es la única combinación que mejora el acierto de A en el subconjunto común (2/3 contra 0/3). Tiene 0 falsos éxitos y un costo medio de US$ 0,0439, por debajo del techo de 0,07.

Escalar no solo mejoró el acierto. También bajó los pasos medios de 48 a 25 y el p95 de 265 s a 115 s. Una decisión del modelo fuerte en el paso trabado (el desplegable, el filtro equivocado) le ahorra al actor barato decenas de pasos de prueba y error. Por eso el costo por tarea casi no sube.

C queda descartada: su costo medio es de US$ 0,1452 en E1-E3 y de 0,1196 en los 5, por encima del techo. Además, en E1 (la corrida limpia) no avanzó más que Haiku solo.

Límites de la evidencia: B se midió una sola vez en 3 casos, así que 2/3 no es una tasa estable. E3 de B costó US$ 0,0626, cerca del techo. El tope `max_task_cost_usd: 0.05` impide escalar en una tarea que ya gastó eso, pero no corta los pasos de Haiku que vienen después. El presupuesto duro sigue siendo `budgets.max_cost_usd`.

## Escalamiento (cómo funciona)

`models.roles.escalation` decide solo la acción del paso trabado. Clasificar, orientar y extraer siguen con sus roles. Hay tres disparadores en `models.escalation.triggers`:

- `sin_efecto`: la misma acción dos veces sin cambiar la pantalla, o la misma acción tres veces sin avanzar. Es el caso del desplegable o el campo que no cambia.
- `cierre_rechazado`: el loop rechazó un `finish`.
- `sin_avance`: `stall_steps` decisiones en la misma fase. Después de escalar, el contador vuelve a empezar.

Cada disparador gasta una sola decisión escalada. Hay dos topes por tarea: `max_steps` decisiones escaladas, y `max_task_cost_usd`, que impide escalar cuando la tarea ya gastó ese monto. Si el rol no está configurado, el agente se comporta como antes. Código: `internal/agent/escalate.go`. Tests con LLM falso: `internal/agent/escalate_test.go`.
