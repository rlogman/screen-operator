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
