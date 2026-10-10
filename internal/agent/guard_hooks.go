package agent

// Puntos de enganche opcionales de los guardrails (epica 7). Un Guard puede
// implementar cualquiera de estas interfaces ademas de Guard; el loop las
// consulta solo si estan. Asi las reglas viven en internal/guard y el loop
// solo les entrega lo que observa.

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"strings"

	"screenoperator/internal/llm"
	"screenoperator/internal/trace"
)

// GuardFailure es el error que devuelve doScreenAction cuando el guardrail
// pide terminar la tarea fallida (Decision.Fail).
type GuardFailure struct{ Decision Decision }

func (e *GuardFailure) Error() string { return e.Decision.Reason }

// PageObserver recibe cada pagina extraida, con la accion que la pidio. Es la
// relectura que usan la unicidad (candidatos), el anti-duplicado y la
// verificacion en codigo.
type PageObserver interface {
	ObservePage(st *State, a Action, p Page)
}

// FinishReviewer revisa una solicitud de finish antes de que el loop la
// procese. Si devuelve un texto, el finish no se procesa: el texto va al
// modelo como indicacion y la tarea sigue (p. ej. "reintenta la busqueda sin
// tilde antes de concluir que no existe").
type FinishReviewer interface {
	ReviewFinish(st *State, f *FinishRequest) string
}

// LabelWanter decide si antes de un click hay que leer la etiqueta del
// elemento (zoom + lector) para saber si el click persiste.
type LabelWanter interface {
	WantLabel(st *State, a Action) bool
}

// DialogCheck es lo que el lector vio despues de un commit.
type DialogCheck struct {
	Checked bool   // se pudo consultar
	Pending bool   // hay un dialogo que pide confirmar o cancelar
	Text    string // texto del dialogo
}

// CommitObserver recibe cada commit ejecutado y si quedo un dialogo de
// confirmacion pendiente en la pantalla.
type CommitObserver interface {
	AfterCommit(st *State, a Action, d DialogCheck)
}

// SecretRedactor redacta secretos (clave del escritorio, tokens, valores
// junto a etiquetas sensibles vistos en pantalla) del resumen, los logs, el
// historial y la traza.
type SecretRedactor interface {
	Redact(s string) string
}

func (a *Agent) redact(s string) string {
	if r, ok := a.Guard.(SecretRedactor); ok {
		return r.Redact(s)
	}
	return s
}

// readLabel lee el texto del elemento bajo (x,y) con un zoom de la region y
// una llamada corta al lector.
func (a *Agent) readLabel(ctx context.Context, x, y int) (string, error) {
	if a.full == nil {
		return "", fmt.Errorf("sin captura")
	}
	r := image.Rect(x-120, y-30, x+120, y+30).Intersect(image.Rect(0, 0, a.full.ScreenW, a.full.ScreenH))
	if r.Dx() < 8 || r.Dy() < 8 {
		return "", fmt.Errorf("region vacia")
	}
	img, err := a.Screen.Zoom(ctx, r)
	if err != nil {
		return "", err
	}
	b, mime, err := a.Cfg.Perception.Encode(img)
	if err != nil {
		return "", err
	}
	var out struct {
		Texto string `json:"texto"`
	}
	start := a.Now()
	u, err := a.askSmall(ctx, "etiqueta", render("label", nil), b, mime, func(s string) error {
		return json.Unmarshal([]byte(llm.StripFences(s)), &out)
	})
	a.traceStep(trace.Step{Phase: string(a.st.Phase), Action: "leer etiqueta", Reason: truncate(out.Texto, 80)}, u, a.Now().Sub(start))
	return strings.TrimSpace(out.Texto), err
}

// checkDialog pregunta al lector si la pantalla actual muestra un dialogo
// que pide confirmar una accion pendiente.
func (a *Agent) checkDialog(ctx context.Context) DialogCheck {
	if a.full == nil {
		return DialogCheck{}
	}
	b, mime, err := a.Cfg.Perception.Encode(a.full.Image)
	if err != nil {
		return DialogCheck{}
	}
	var out struct {
		Dialogo bool   `json:"dialogo"`
		Texto   string `json:"texto"`
	}
	start := a.Now()
	u, err := a.askSmall(ctx, "dialogo", render("dialog", nil), b, mime, func(s string) error {
		return json.Unmarshal([]byte(llm.StripFences(s)), &out)
	})
	a.traceStep(trace.Step{Phase: string(a.st.Phase), Action: fmt.Sprintf("revisar dialogo: %v", out.Dialogo), Reason: truncate(out.Texto, 120)}, u, a.Now().Sub(start))
	if err != nil {
		return DialogCheck{}
	}
	return DialogCheck{Checked: true, Pending: out.Dialogo, Text: strings.TrimSpace(out.Texto)}
}

// askSmall es una llamada de lectura con una imagen y sin reparacion.
func (a *Agent) askSmall(ctx context.Context, purpose, task string, img []byte, mime string, parse func(string) error) (llm.Usage, error) {
	sys := llm.Message{Role: "system", Parts: []llm.Part{llm.TextPart(ExtractSystemPrompt())}}
	user := llm.Message{Role: "user", Parts: []llm.Part{llm.TextPart(task), llm.ImagePart(img, mime)}}
	role := a.Cfg.Extractor
	max := role.MaxTokens
	if max <= 0 || max > 300 {
		max = 300
	}
	resp, err := a.LLM.Chat(ctx, llm.Request{Model: role.Model, Temperature: role.Temperature, MaxTokens: max, JSON: true,
		Purpose: purpose, ReasoningEffort: role.Reasoning, Messages: []llm.Message{sys, user}})
	if err != nil {
		return resp.Usage, err
	}
	return resp.Usage, parse(resp.Content)
}

// redactHandler redacta con el guardrail cada mensaje y atributo de texto.
type redactHandler struct {
	next slog.Handler
	r    SecretRedactor
}

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Redact(rec.Message), rec.PC)
	rec.Attrs(func(at slog.Attr) bool {
		out.AddAttrs(h.attr(at))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) attr(at slog.Attr) slog.Attr {
	if at.Value.Kind() == slog.KindString {
		at.Value = slog.StringValue(h.r.Redact(at.Value.String()))
	}
	return at
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, at := range attrs {
		red[i] = h.attr(at)
	}
	return &redactHandler{next: h.next.WithAttrs(red), r: h.r}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{next: h.next.WithGroup(name), r: h.r}
}
