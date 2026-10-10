package agent

// Escalamiento de modelo (historia 8.2): el actor barato decide casi todos los
// pasos; cuando el loop se traba, la siguiente decision la toma el rol
// models.roles.escalation. Disparadores (models.escalation.triggers):
//   - sin_efecto: la misma accion se repitio sin cambiar la pantalla, o se
//     repitio 3 veces sin avanzar (un desplegable o campo que no cambia);
//   - cierre_rechazado: el loop rechazo un "finish";
//   - sin_avance: stall_steps decisiones seguidas en la misma fase.
// Tope: max_steps decisiones escaladas por tarea y, si max_task_cost_usd > 0,
// no se escala cuando la tarea ya gasto eso. Sin el rol, nunca se escala.

import (
	"fmt"

	"screenoperator/internal/llm"
)

// Valores por defecto de models.escalation cuando el rol existe.
const (
	defaultEscalationStall = 8
	defaultEscalationMax   = 3
)

// escalator lleva las senales de traba y el conteo de pasos escalados.
type escalator struct {
	pending    string // disparador pendiente ("" = ninguno)
	phase      Phase  // fase observada en la ultima decision
	phaseSince int    // paso en que se entro a esa fase (o se rearmo sin_avance)
	used       int    // decisiones escaladas en la tarea
	spent      float64
}

func (a *Agent) escalationOn() bool { return a.Cfg.Escalation.Model != "" }

func (a *Agent) escTrigger(name string) bool {
	if !a.escalationOn() {
		return false
	}
	ts := a.Cfg.EscalationPolicy.Triggers
	if len(ts) == 0 {
		return true // sin lista: todos los disparadores
	}
	for _, t := range ts {
		if t == name {
			return true
		}
	}
	return false
}

// escSignal registra una senal de traba (sin_efecto o cierre_rechazado).
func (a *Agent) escSignal(name string) {
	if a.escTrigger(name) && a.esc.pending == "" {
		a.esc.pending = name
	}
}

// actorRole elige el rol de la decision del paso actual: el de escalamiento
// si hay una traba pendiente y queda cupo, o el actor.
func (a *Agent) actorRole() llm.Role {
	st := a.st
	if st.Phase != a.esc.phase {
		a.esc.phase, a.esc.phaseSince = st.Phase, st.Steps
	}
	if !a.escalationOn() {
		return a.Cfg.Actor
	}
	pol := a.Cfg.EscalationPolicy
	stall := pol.StallSteps
	if stall <= 0 {
		stall = defaultEscalationStall
	}
	why := a.esc.pending
	if why == "" && a.escTrigger(llm.TriggerStall) && st.Steps-a.esc.phaseSince >= stall {
		why = llm.TriggerStall
	}
	if why == "" {
		return a.Cfg.Actor
	}
	a.esc.pending = ""
	if why == llm.TriggerStall {
		a.esc.phaseSince = st.Steps // rearmar: otros N pasos antes de volver a escalar
	}
	maxSteps := pol.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultEscalationMax
	}
	if a.esc.used >= maxSteps {
		return a.Cfg.Actor
	}
	if pol.MaxTaskCostUSD > 0 && a.esc.spent >= pol.MaxTaskCostUSD {
		a.logInfo("escalamiento omitido por costo", "disparador", why, "costo_usd", a.esc.spent)
		return a.Cfg.Actor
	}
	a.esc.used++
	st.Escalated = append(st.Escalated, fmt.Sprintf("%d:%s", st.Steps, why))
	a.logInfo("paso escalado", "paso", st.Steps, "disparador", why, "modelo", a.Cfg.Escalation.Model,
		"escalados", a.esc.used, "max", maxSteps)
	return a.Cfg.Escalation
}
