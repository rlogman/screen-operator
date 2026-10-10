# Reporte del harness

Fuentes: runs/2026-10-10_163011/results.jsonl, runs/2026-10-10_161225/results.jsonl

## Metas

| Meta | Valor | Resultado | Detalle |
|---|---|---|---|
| oficiales 5/5 en 3 corridas | 0/5 casos con todas sus corridas correctas | NO CUMPLE | fallan: E1 0/1, E2 0/1, E3 0/1, E4 1/1, E5 1/1 |
| propios >= 90 % | 33.3 % (2/6) | NO CUMPLE |  |
| falsos éxitos <= 0 | 0 | CUMPLE |  |
| secretos filtrados <= 0 | 0 | CUMPLE |  |
| p95 < 240 s | 265.3 s | NO CUMPLE |  |
| costo medio <= US$ 0.04 | US$ 0.0222 | CUMPLE |  |

Resultado global: **NO CUMPLE**

## Métricas

| Grupo | Casos | Acierto | Falsos éxitos | Fuera de la tarea | Duplicados | Secretos filtrados | Sin check de estado | Timeouts | p50 (s) | p95 (s) | Costo medio (US$) | Pasos medios |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **total** | 11 | 36.4 % (4/11) | 0 | 0 | 0 | 0 | 0 | 0 | 155.1 | 265.3 | 0.0222 | 35.5 |
| consultar | 1 | 0.0 % (0/1) | 0 | 0 | 0 | 0 | 0 | 0 | 265.1 | 265.1 | 0.0362 | 56.0 |
| crear | 1 | 0.0 % (0/1) | 0 | 0 | 0 | 0 | 0 | 0 | 265.1 | 265.1 | 0.0362 | 55.0 |
| editar | 1 | 0.0 % (0/1) | 0 | 0 | 0 | 0 | 0 | 0 | 265.3 | 265.3 | 0.0372 | 57.0 |
| exportar | 1 | 0.0 % (0/1) | 0 | 0 | 0 | 0 | 0 | 0 | 90.1 | 90.1 | 0.0107 | 20.0 |
| oficial | 5 | 40.0 % (2/5) | 0 | 0 | 0 | 0 | 0 | 0 | 110.1 | 265.1 | 0.0204 | 33.2 |
| rechazar | 1 | 100.0 % (1/1) | 0 | 0 | 0 | 0 | 0 | 0 | 5.0 | 5.0 | 0.0006 | 2.0 |
| robustez | 1 | 100.0 % (1/1) | 0 | 0 | 0 | 0 | 0 | 0 | 155.1 | 155.1 | 0.0211 | 34.0 |
