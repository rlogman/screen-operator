# Decisiones autónomas: screen-operator-v1

Formato: `fecha hora BOG · épica/historia · decisión · alternativa descartada · razón`

- 2026-10-10 11:40 · global · Trazabilidad Gherkin→test por convención de nombres `TestE<n>_<m>_S<k>_*`, verificada por `scripts/gherkin_coverage.py` · godog (Cucumber para Go) · evita una dependencia y una capa de step definitions; el gate es determinista y rápido.
- 2026-10-10 11:40 · global · Tests contra el escritorio real y OpenRouter detrás de build tags `desktop` y `live` · correrlos siempre · la suite por defecto queda rápida y sin costo; los reales se corren en el gate de las épicas que los necesitan.
- 2026-10-10 11:40 · global · Perfil de quality gates `prototype` con el lente atacante activo · `production` · hackathon de un día; se manejan credenciales reales, por eso el atacante sigue siendo bloqueante.
- 2026-10-10 11:40 · épica 4 · Despliegue temprano (épica 4) antes del harness y del agente · desplegar al final · el validador del entrypoint del evento y la plataforma se prueban con tiempo; el agente se actualiza después sin cambiar el contrato.
