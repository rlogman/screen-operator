# Decisiones autónomas: screen-operator-v1

Formato: `fecha hora BOG · épica/historia · decisión · alternativa descartada · razón`

- 2026-10-10 11:40 · global · Trazabilidad Gherkin→test por convención de nombres `TestE<n>_<m>_S<k>_*`, verificada por `scripts/gherkin_coverage.py` · godog (Cucumber para Go) · evita una dependencia y una capa de step definitions; el gate es determinista y rápido.
- 2026-10-10 11:40 · global · Tests contra el escritorio real y OpenRouter detrás de build tags `desktop` y `live` · correrlos siempre · la suite por defecto queda rápida y sin costo; los reales se corren en el gate de las épicas que los necesitan.
- 2026-10-10 11:40 · global · Perfil de quality gates `prototype` con el lente atacante activo · `production` · hackathon de un día; se manejan credenciales reales, por eso el atacante sigue siendo bloqueante.
- 2026-10-10 11:40 · épica 4 · Despliegue temprano (épica 4) antes del harness y del agente · desplegar al final · el validador del entrypoint del evento y la plataforma se prueban con tiempo; el agente se actualiza después sin cambiar el contrato.
- 2026-10-10 12:00 · épica 1/1.1 · Los tests de `make gate` y `make test-epic` corren el Makefile real copiado a un módulo Go temporal (casos pasa / gofmt / vet / test fallan) · correr `make gate` sobre el propio repo desde un test · desde `go test` eso recursa sin fin; el módulo temporal prueba el mismo Makefile de forma determinista.
- 2026-10-10 12:00 · épica 1/1.1 · Tests de repo (Makefile, .gitignore) en `gates_test.go` en la raíz, paquete solo de tests · un paquete `internal/gates` · no agrega un paquete fuera de la lista del diseño.
- 2026-10-10 12:00 · épica 1/1.1 · `cmd/agent`, `cmd/harness` y `cmd/rfbprobe` arrancan como stubs que salen con código 2 y dicen en qué épica se implementan · stubs que salen con 0 · un binario que no hace nada no debe parecer sano.
