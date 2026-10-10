package agent

import (
	"fmt"
	"strings"
)

// Decision es la respuesta de un guardrail. Deny=false deja seguir.
type Decision struct {
	Deny        bool
	Reason      string
	Identifiers []string // identificadores que explican el rechazo (van al resumen)
	// Fail (con Deny) termina la tarea "fallida" con Reason e Identifiers en
	// vez de devolverle la razon al modelo (epica 7: unicidad, duplicado).
	Fail bool
	// Commit (sin Deny) indica que la accion, aunque el modelo la pidio como
	// click, persiste (su etiqueta es Guardar, Exportar...): el loop la
	// registra como commit sobre Target.
	Commit bool
	Target string
}

// Allow deja seguir.
var Allow = Decision{}

// Deny rechaza con una razon.
func Deny(format string, a ...any) Decision {
	return Decision{Deny: true, Reason: fmt.Sprintf(format, a...)}
}

// Fail termina la tarea fallida con una razon y los identificadores.
func Fail(ids []string, format string, a ...any) Decision {
	return Decision{Deny: true, Fail: true, Reason: fmt.Sprintf(format, a...), Identifiers: ids}
}

// Guard es el punto de enganche de los guardrails en codigo (epica 7). El
// loop lo consulta:
//   - Classified: al terminar de clasificar, antes de tocar la app. Un rechazo
//     termina la tarea "fallida" sin enviar ningun evento al driver.
//   - BeforeAction: antes de cada accion que envia eventos (click, type, key,
//     scroll, hover, commit...). Un rechazo no toca el driver; el modelo recibe
//     la razon en el historial y decide otra cosa.
//   - BeforeFinish: antes de aceptar finish (ya verificado). Un rechazo deja la
//     tarea "fallida" con la razon y los identificadores.
//
// Las reglas viven aqui y no en el prompt: el modelo no puede saltarselas.
type Guard interface {
	Classified(st *State) Decision
	BeforeAction(st *State, a Action) Decision
	BeforeFinish(st *State, f *FinishRequest) Decision
}

// DefaultGuard es la implementacion de la epica 6: permite todo salvo una
// clasificacion "rechazar" (E6.3-S5). La epica 7 la reemplaza por las reglas
// completas (palabras de policy.destructive, unicidad, anti-duplicado, commit
// unico, verificacion en codigo).
type DefaultGuard struct{}

// Classified rechaza la intencion "rechazar" y las escrituras con datos faltantes.
func (DefaultGuard) Classified(st *State) Decision {
	c := st.Class
	if c.Intent == IntentReject {
		r := c.RejectReason
		if r == "" {
			r = "la orden es peligrosa o imposible de cumplir"
		}
		if len(c.Missing) > 0 {
			r += "; faltan: " + strings.Join(c.Missing, ", ")
		}
		return Deny("orden rechazada sin tocar la aplicación: %s", r)
	}
	return Allow
}

// BeforeAction permite todo.
func (DefaultGuard) BeforeAction(*State, Action) Decision { return Allow }

// BeforeFinish permite todo.
func (DefaultGuard) BeforeFinish(*State, *FinishRequest) Decision { return Allow }
