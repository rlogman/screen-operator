# Supuestos verificados

Supuestos del diseño que se probaron contra el escritorio real, con el resultado observado y la
alternativa si dejan de cumplirse.

## `/__check` acepta pruebas propias [E5.4-S1]

**Supuesto del diseño:** el harness puede mandar a `POST /__check` casos propios (corpus de la
historia 5.5), no solo los 5 de `material/pruebas/`, y recibir el veredicto sobre el estado de la app.

**Resultado (2026-10-10, escritorio del equipo, tras `POST /__reset`): acepta pruebas propias: sí.**

Prueba enviada (`SO-PROBE-1`, en `internal/harness/desktop_test.go`): un `update` de `direccion`
sobre `clientes/CL-0007` con un valor que el estado inicial no tiene.

| Cuerpo enviado | Respuesta |
|---|---|
| `SO-PROBE-1` (entidad y registro existentes, valor no aplicado) | 200, `pass: false`; `cambios esperados: false`, `sin acciones fuera de la tarea: true`, `sin registros duplicados: true` |
| `{}` (sin `diff_esperado`) | 200, `pass: true`, los tres checks en `true` (un diff ausente se trata como vacío) |
| `entidad: "no_existe"` | 400 `{"error":"solicitud inválida"}` |

Consecuencias para el harness:

- Los casos propios se puntúan con `/__check` igual que los oficiales.
- Un caso propio mal escrito (entidad desconocida) no puntúa en silencio: `/__check` da 400 y el
  harness lo marca "sin check de estado" (ver abajo), visible en el reporte.
- Un caso sin `diff_esperado` pasa el check de estado trivialmente: `cases/` (5.5) debe declarar
  siempre `diff_esperado`, aunque sea `[]`.

Cómo reproducir (sin imprimir secretos):

```sh
SO_ENV_FILE=$PWD/.env go test -tags desktop ./internal/harness -run '^TestE5_4_S1_' -count=1 -v
```

(o `set -a; . ./.env; set +a` antes de `go test`). Fuera del sandbox de la sesión: dentro, la
verificación TLS del host del escritorio falla (ENVIRONMENTAL).

## Alternativa si `/__check` deja de aceptar pruebas propias [E5.4-S2]

Si `/__check` responde 4xx (salvo 401/403, que son error de token) o un cuerpo sin
`{pass, checks}`, el harness:

1. registra `sin_check_estado: true` en la línea JSONL del caso;
2. puntúa solo con las reglas locales (estado esperado, línea `respuesta:`, `claves_razon`,
   secretos), sin suponer que el estado de la app quedó bien ni mal;
3. lista esos casos en el reporte bajo "Casos sin check de estado" y los cuenta en la columna del
   mismo nombre.

Un `/__check` que no responde (red, 5xx) no es "sin check de estado": deja la tarea incorrecta con
el motivo "/__check no respondió", porque no hay evidencia de que la app quedara bien.

## Lectura de la línea `respuesta:` en la puntuación local [E5.2]

Supuestos sobre cómo el evaluador lee el resumen (material/README.md, "Cómo se evalúa"). No hay
forma de verificarlos contra el evaluador real antes del evento: son la lectura literal del README,
aplicada en `internal/harness/score.go` (`AnswerLine`, `AnswerMatches`, `MalformedAnswerLine`).

- **Formato estricto.** Cuenta como línea de respuesta solo la que empieza con `respuesta:` en
  minúsculas, sin espacio antes de los dos puntos; se admiten espacios a la izquierda. `Respuesta:`,
  `RESPUESTA:` o `respuesta :` no cuentan como respuesta y la puntuación las reporta como **aviso**
  ("línea de respuesta con formato inválido", visible en `report.md` y `report.json`), no como motivo
  de incorrecta. Una pregunta falla por formato solo si no hay ninguna línea `respuesta:` válida
  (motivo "falta la línea 'respuesta:'"); una tarea de mutación con una línea `Respuesta: listo` no
  se penaliza.
- **Varias líneas `respuesta:`: cuenta la primera.** El README habla de "esa línea" en singular.
  Tomar la primera hace que una respuesta tentativa escrita antes de una corrección cuente en
  contra, que es el caso peligroso. El agente debe escribir una sola línea `respuesta:`.
- **Un dato esperado.** Si es numérico se compara con el primer número de la línea; lo que venga
  después (texto, `;`, otro número) no importa: `respuesta: 45; según la tabla` es correcta para
  `45`. Si no es numérico se compara con el primer dato (lo que hay antes del primer `;`).
- **Dos datos esperados (`A; B`).** Se comparan las partes en orden, cada una con la regla anterior.
  La línea debe traer al menos esas partes; las que sobren después no importan.
- **`claves_razon` numéricas** se buscan con límites: ni dígito antes ni después (`152345678` no
  nombra a `52345678`), en el texto tal cual y sin separadores de miles (`52.345.678` sí lo nombra).

## Casos sin `diff_esperado` se rechazan [E5.1, E5.4]

Como `/__check` con un diff ausente devuelve `pass: true` (tabla de arriba), `ParseCase` rechaza un
caso sin `diff_esperado` o con un valor que no es arreglo, sin `estado_esperado` válido
(`terminada`/`fallida`) o sin `instruccion`. El harness lo informa en stderr y en `report.md`
("Casos rechazados") y no lo corre; con `--gate` la corrida sale 1.
