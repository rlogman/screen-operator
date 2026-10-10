package guard

import "screenoperator/internal/agent"

// New devuelve el guardrail que usa el agente en cada tarea. La interfaz es
// agent.Guard (consultada antes de cada accion que envia eventos, al terminar
// de clasificar y antes de aceptar finish).
//
// Epica 6: agent.DefaultGuard (rechaza la intencion "rechazar" sin tocar el
// driver). La epica 7 agrega aqui las reglas completas: palabras de
// policy.destructive, unicidad del objetivo, anti-duplicado, commit unico y
// verificacion en codigo.
func New() agent.Guard { return agent.DefaultGuard{} }
