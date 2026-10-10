package summary

// Formateador determinista del resumen (6.6). El modelo nunca escribe la linea
// de respuesta libremente: la arma este codigo con summary.* de la
// configuracion. La redaccion de secretos ocurre despues, en el Task Manager.

import (
	"fmt"
	"regexp"
	"strings"

	"screenoperator/internal/config"
)

// Format es summary.* de la configuracion.
type Format struct {
	AnswerPrefix         string `json:"answer_line_prefix"`
	Separator            string `json:"multi_value_separator"`
	NumberFirst          bool   `json:"number_first"`
	OmitWhenUnanswerable bool   `json:"omit_answer_when_unanswerable"`
	NameIdentifiers      bool   `json:"failure_must_name_identifiers"`
}

// FromConfig lee summary.*.
func FromConfig(c *config.Config) (Format, error) {
	f := Format{NumberFirst: true, OmitWhenUnanswerable: true, NameIdentifiers: true}
	if err := c.Decode("summary", &f); err != nil {
		return f, fmt.Errorf("summary: %w", err)
	}
	return f, nil
}

// Report es lo que el agente sabe al terminar.
type Report struct {
	Done        bool     // terminada (true) o fallida
	Intent      string   // crear, editar, consultar, exportar, rechazar
	Entity      string   // cliente, pedido...
	EntityID    string   // identificador del registro tocado
	Target      string   // nombre buscado (si no hay identificador)
	Changed     []string // "campo = valor"
	Question    bool     // la orden es una pregunta
	Answer      []string // datos de la respuesta en el orden pedido; nil = sin respuesta
	Reason      string   // por que (obligatoria en fallidas)
	Identifiers []string // identificadores que explican una falla
	Notes       []string // detalles adicionales (interpretaciones, verificacion)
	Steps       int
}

// Build arma el resumen. Reglas:
//   - Una pregunta terminada con datos lleva UNA linea "respuesta: v1; v2" en
//     el orden pedido; si el valor es un numero, va primero.
//   - Una fallida, o una pregunta sin datos, no lleva linea de respuesta.
//   - Una fallida nombra la razon y todos los identificadores dados.
//   - Un cambio terminado nombra entidad, identificador y campos cambiados.
//   - Ninguna otra linea puede empezar con el prefijo de respuesta: los textos
//     libres se aplanan a una linea y se neutraliza el prefijo.
func (f Format) Build(r Report) string {
	prefix := f.AnswerPrefix
	if prefix == "" {
		prefix = "respuesta: "
	}
	sep := f.Separator
	if sep == "" {
		sep = "; "
	}
	var lines []string
	if r.Done && r.Question && len(nonEmpty(r.Answer)) > 0 {
		lines = append(lines, prefix+f.answer(r.Answer, sep))
	}
	clean := func(s string) string { return f.flatten(s, prefix) }

	what := describe(r)
	if r.Done {
		lines = append(lines, clean("Tarea terminada: "+what+"."))
	} else {
		lines = append(lines, clean("Tarea fallida: "+what+"."))
	}
	if len(r.Changed) > 0 {
		lines = append(lines, clean("Campos cambiados: "+strings.Join(r.Changed, "; ")+"."))
	}
	if r.Reason != "" {
		lines = append(lines, clean("Razón: "+strings.TrimRight(r.Reason, ". ")+"."))
	}
	if !r.Done && len(r.Identifiers) > 0 {
		lines = append(lines, clean("Identificadores: "+strings.Join(r.Identifiers, ", ")+"."))
	}
	for _, n := range r.Notes {
		if strings.TrimSpace(n) != "" {
			lines = append(lines, clean(n))
		}
	}
	return strings.Join(lines, "\n")
}

func describe(r Report) string {
	var parts []string
	verb := map[string]string{"crear": "crear", "editar": "editar", "consultar": "consultar", "exportar": "exportar", "rechazar": "orden rechazada"}[r.Intent]
	if verb == "" {
		verb = strings.TrimSpace(r.Intent)
	}
	if verb != "" {
		parts = append(parts, verb)
	}
	if r.Entity != "" {
		parts = append(parts, r.Entity)
	}
	if r.Target != "" && !strings.EqualFold(r.Target, r.EntityID) {
		parts = append(parts, r.Target)
	}
	if r.EntityID != "" {
		parts = append(parts, "("+r.EntityID+")")
	}
	if len(parts) == 0 {
		return "sin detalle"
	}
	return strings.Join(parts, " ")
}

var (
	reNumberStart = regexp.MustCompile(`^-?\d`)
	reNumber      = regexp.MustCompile(`-?\d[\d.,]*`)
)

// answer arma los datos de la linea de respuesta.
func (f Format) answer(vals []string, sep string) string {
	vals = nonEmpty(vals)
	sepCore := strings.TrimSpace(sep)
	out := make([]string, len(vals))
	for i, v := range vals {
		v = strings.Join(strings.Fields(v), " ")
		if sepCore != "" {
			// Un separador dentro de un dato partiria la respuesta.
			v = strings.ReplaceAll(v, sepCore, ",")
		}
		out[i] = v
	}
	if f.NumberFirst && len(out) > 0 && !reNumberStart.MatchString(out[0]) {
		// "Total: 45" -> "45 (Total: 45)": el evaluador compara el primer numero.
		if n := reNumber.FindString(out[0]); n != "" && len(vals) == 1 {
			out[0] = strings.TrimRight(n, ".,") + " (" + out[0] + ")"
		}
	}
	return strings.Join(out, sep)
}

// flatten deja un texto libre en una sola linea que no empieza con el prefijo.
func (f Format) flatten(s, prefix string) string {
	s = strings.Join(strings.Fields(s), " ")
	p := strings.ToLower(strings.TrimSpace(prefix))
	if p != "" && strings.HasPrefix(strings.ToLower(s), p) {
		s = "(" + s + ")"
	}
	return s
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}
