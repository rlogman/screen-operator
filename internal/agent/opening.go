package agent

// Revision de apertura (epica 10, incidente FR-0153): antes de la primera
// accion, el agente lee la huella de la app (titulo y version visible) y
// revisa si un dialogo bloquea la pantalla. Un dialogo informativo se cierra;
// uno con efecto nunca se confirma: se cancela si hay una opcion segura y, si
// no la hay, la tarea termina fallida nombrando el dialogo.
//
// Generico: las etiquetas de botones y palabras de efecto vienen de
// policy.dialogs; nada de textos ni coordenadas de una app concreta.

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"unicode"

	"screenoperator/internal/config"
	"screenoperator/internal/llm"
	"screenoperator/internal/trace"
)

// OpeningPromptVersion identifica prompts/opening_v1.tmpl.
const OpeningPromptVersion = "opening-v1"

//go:embed prompts/opening_v1.tmpl
var openingFS embed.FS

var openingPrompts = template.Must(template.New("opening_v1.tmpl").Option("missingkey=error").ParseFS(openingFS, "prompts/opening_v1.tmpl"))

func renderOpening() string {
	var b bytes.Buffer
	if err := openingPrompts.ExecuteTemplate(&b, "opening", nil); err != nil {
		panic(fmt.Sprintf("agent: plantilla opening: %v", err))
	}
	return b.String()
}

// DialogPolicy es policy.dialogs: como se tratan los dialogos al abrir.
type DialogPolicy struct {
	Enabled      bool     `json:"enabled"`
	MaxOnOpen    int      `json:"max_on_open"`
	CloseLabels  []string `json:"close_labels"`
	CancelLabels []string `json:"cancel_labels"`
	EffectWords  []string `json:"effect_words"`
}

// DialogPolicyFromConfig lee policy.dialogs (ausente = deshabilitado).
func DialogPolicyFromConfig(c *config.Config) (DialogPolicy, error) {
	var p DialogPolicy
	if c == nil {
		return p, nil
	}
	if _, ok := c.Lookup("policy.dialogs"); !ok {
		return p, nil
	}
	if err := c.Decode("policy.dialogs", &p); err != nil {
		return DialogPolicy{}, err
	}
	if p.MaxOnOpen == 0 && p.Enabled {
		p.MaxOnOpen = 3
	}
	return p, nil
}

// AppMatch es lo que el agente sabe del perfil de app tras la huella.
type AppMatch struct {
	ID     string // perfil aplicado ("" = generico)
	Hints  string // pistas para el prompt
	Change string // "cambio de app detectado: v3.9 -> v4.2" (perfil descartado)
}

// Dialog es un dialogo leido en pantalla.
type Dialog struct {
	Title   string         `json:"titulo"`
	Text    string         `json:"texto"`
	Kind    string         `json:"tipo"`
	Buttons []DialogButton `json:"botones"`
}

// DialogButton es un boton del dialogo, con coordenadas de la imagen.
type DialogButton struct {
	Label string  `json:"texto"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type openingReply struct {
	AppTitle string  `json:"titulo_app"`
	Version  *string `json:"version"`
	Dialog   *Dialog `json:"dialogo"`
}

func parseOpening(s string) (openingReply, error) {
	var o openingReply
	if err := json.Unmarshal([]byte(llm.StripFences(s)), &o); err != nil {
		return o, &ParseError{Msg: "no es un objeto JSON: " + shorten(err.Error())}
	}
	if o.Dialog != nil && strings.TrimSpace(o.Dialog.Title) == "" && strings.TrimSpace(o.Dialog.Text) == "" && len(o.Dialog.Buttons) == 0 {
		o.Dialog = nil // {} vacio = sin dialogo
	}
	return o, nil
}

// DialogDecision es lo que el codigo decide hacer con un dialogo.
type DialogDecision struct {
	Effect bool          // el dialogo pide una accion con efecto
	Button *DialogButton // boton seguro para cerrarlo o cancelarlo (nil = no hay)
}

// foldLabel pasa a minusculas, quita tildes y signos de los bordes.
func foldLabel(s string) string {
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", "Ñ", "n")
	s = strings.ToLower(r.Replace(strings.TrimSpace(s)))
	return strings.TrimFunc(s, func(c rune) bool { return unicode.IsPunct(c) || unicode.IsSpace(c) })
}

func words(s string) []string {
	return strings.FieldsFunc(foldLabel(s), func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) })
}

// hasEffectWord dice si s contiene alguna palabra de efecto (palabra completa;
// las de 5 letras o mas tambien como prefijo: "eliminarlo", "confirmacion").
func (p DialogPolicy) hasEffectWord(s string) bool {
	for _, w := range words(s) {
		for _, e := range p.EffectWords {
			e = foldLabel(e)
			if e == "" {
				continue
			}
			if w == e || (len(e) >= 5 && strings.HasPrefix(w, e)) {
				return true
			}
		}
	}
	return false
}

func inLabels(label string, labels []string) bool {
	l := foldLabel(label)
	for _, x := range labels {
		if foldLabel(x) == l {
			return true
		}
	}
	return false
}

// Decide clasifica el dialogo y elige el boton seguro. Es informativo solo si
// el modelo lo leyo asi Y ni el titulo ni ningun boton tienen una palabra de
// efecto. Informativo: boton de close_labels o, si hay un unico boton sin
// efecto, ese. Con efecto: solo un boton de cancel_labels sin palabra de
// efecto; nunca uno que confirme.
func (p DialogPolicy) Decide(d Dialog) DialogDecision {
	effect := foldLabel(d.Kind) != "informativo" || p.hasEffectWord(d.Title)
	for _, b := range d.Buttons {
		if p.hasEffectWord(b.Label) {
			effect = true
		}
	}
	// Se prefiere un boton con texto a la "x" de la esquina (blanco chico).
	pick := func(labels []string) *DialogButton {
		for _, corner := range []bool{false, true} {
			for i := range d.Buttons {
				b := d.Buttons[i]
				isCorner := foldLabel(b.Label) == "x" || foldLabel(b.Label) == "×" || foldLabel(b.Label) == ""
				if isCorner == corner && inLabels(b.Label, labels) && !p.hasEffectWord(b.Label) {
					return &b
				}
			}
		}
		return nil
	}
	if effect {
		return DialogDecision{Effect: true, Button: pick(p.CancelLabels)}
	}
	if b := pick(p.CloseLabels); b != nil {
		return DialogDecision{Button: b}
	}
	if len(d.Buttons) == 1 && foldLabel(d.Buttons[0].Label) != "" {
		b := d.Buttons[0]
		return DialogDecision{Button: &b}
	}
	return DialogDecision{}
}

// openingFailure lo devuelve openingCheck cuando la tarea debe terminar.
type openingFailure struct {
	reason string
	ids    []string
}

// openingCheck corre la revision de apertura sobre la vista actual: devuelve
// el texto de huella (titulo y version de la app) para resolver el perfil, y
// una falla si un dialogo con efecto no se puede cerrar de forma segura. Los
// errores del modelo no detienen la tarea (el loop tambien ve los dialogos),
// salvo presupuesto agotado o contexto vencido.
func (a *Agent) openingCheck(ctx context.Context) (string, *openingFailure, error) {
	p := a.Cfg.Dialogs
	var fingerprint string
	escaped := false
	for round := 0; round <= p.MaxOnOpen; round++ {
		if a.view == nil || a.view.Zoomed {
			if _, err := a.stableCapture(ctx); err != nil {
				return fingerprint, nil, err
			}
		}
		v := *a.view
		start := a.Now()
		msg, shot, err := a.userMessage(renderOpening(), "", v)
		if err != nil {
			return fingerprint, nil, err
		}
		var o openingReply
		u, err := a.ask(ctx, "apertura", a.Cfg.Actor, msg, shot, func(s string) error {
			var perr error
			o, perr = parseOpening(s)
			return perr
		})
		if err != nil {
			a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "revisar apertura", Reason: "error: " + errText(err)}, u, a.Now().Sub(start))
			if errors.Is(err, llm.ErrBudget) || ctx.Err() != nil {
				return fingerprint, nil, err
			}
			a.logInfo("revision de apertura sin respuesta valida; sigue el loop", "error", errText(err))
			return fingerprint, nil, nil
		}
		if round == 0 {
			parts := []string{o.AppTitle}
			if o.Version != nil && strings.TrimSpace(*o.Version) != "" {
				parts = append(parts, "version "+config.NormVersion(*o.Version))
			}
			fingerprint = strings.TrimSpace(strings.Join(parts, "\n"))
		}
		if o.Dialog == nil {
			reason := "sin dialogos que bloqueen la app"
			if round > 0 {
				reason = "la pantalla principal quedo libre de dialogos"
			}
			a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "revisar apertura: " + truncate(o.AppTitle, 80), Reason: reason}, u, a.Now().Sub(start))
			return fingerprint, nil, nil
		}
		d := *o.Dialog
		name := strings.TrimSpace(d.Title)
		if name == "" {
			name = truncate(strings.TrimSpace(d.Text), 60)
		}
		dec := p.Decide(d)
		if round == p.MaxOnOpen {
			// Ultima vuelta: solo se mira; no se vuelve a hacer clic.
			if dec.Effect {
				return fingerprint, a.dialogFailure(name, "sigue abierto tras intentar cancelarlo"), nil
			}
			a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "revisar apertura", Reason: fmt.Sprintf("el dialogo %q sigue abierto; sigue el loop", name)}, u, a.Now().Sub(start))
			a.addNote(fmt.Sprintf("al abrir hay un dialogo %q que no se pudo cerrar", name))
			return fingerprint, nil, nil
		}
		if dec.Button == nil {
			if dec.Effect {
				a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "revisar apertura", Reason: fmt.Sprintf("dialogo con efecto %q sin opcion segura", name)}, u, a.Now().Sub(start))
				return fingerprint, a.dialogFailure(name, "no tiene una opción segura para cancelarlo"), nil
			}
			if escaped {
				a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "revisar apertura", Reason: fmt.Sprintf("dialogo informativo %q sin boton reconocible; sigue el loop", name)}, u, a.Now().Sub(start))
				a.addNote(fmt.Sprintf("al abrir hay un dialogo informativo %q; cierralo antes de seguir", name))
				return fingerprint, nil, nil
			}
			escaped = true
			act := Action{Kind: KindKey, Combo: "escape", Reason: "cerrar dialogo informativo sin boton reconocible"}
			a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "cerrar dialogo: " + act.String(), Reason: fmt.Sprintf("dialogo informativo %q sin boton reconocible: escape", name)}, u, a.Now().Sub(start))
			if err := a.openingAction(ctx, act, name); err != nil {
				return fingerprint, nil, err
			}
			continue
		}
		bx, by := int(dec.Button.X+0.5), int(dec.Button.Y+0.5)
		b := v.Img.Bounds()
		if bx < 0 || by < 0 || bx >= b.Dx() || by >= b.Dy() {
			if dec.Effect {
				return fingerprint, a.dialogFailure(name, "el botón para cancelarlo está fuera de la pantalla"), nil
			}
			a.addNote(fmt.Sprintf("al abrir hay un dialogo informativo %q; cierralo antes de seguir", name))
			return fingerprint, nil, nil
		}
		x, y := v.ToScreen(bx, by)
		label := strings.TrimSpace(dec.Button.Label)
		act := Action{Kind: KindClick, X: x, Y: y, Reason: fmt.Sprintf("cerrar dialogo %q con %q", name, label)}
		what := "diálogo informativo cerrado"
		if dec.Effect {
			what = "diálogo con efecto cancelado sin confirmar"
		}
		a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: fmt.Sprintf("cerrar dialogo: %s %q", act, label),
			Reason: fmt.Sprintf("%s: %q (boton %q)", what, name, label)}, u, a.Now().Sub(start))
		a.logInfo(what, "dialogo", name, "boton", label)
		if err := a.openingAction(ctx, act, name); err != nil {
			return fingerprint, nil, err
		}
		a.record(fmt.Sprintf("(apertura) %s: %q con el boton %q en (%d,%d)", what, name, label, x, y))
	}
	return fingerprint, nil, nil
}

func (a *Agent) dialogFailure(name, why string) *openingFailure {
	return &openingFailure{
		reason: fmt.Sprintf("al abrir la aplicación apareció el diálogo %q, que pide confirmar una acción que la orden no pidió y %s; no se confirmó", name, why),
		ids:    []string{name},
	}
}

// openingAction ejecuta un clic o tecla de cierre pasando por el guardrail y
// deja la vista para recapturar.
func (a *Agent) openingAction(ctx context.Context, act Action, name string) error {
	if d := a.Guard.BeforeAction(a.st, act); d.Deny {
		a.record(fmt.Sprintf("(apertura) cerrar dialogo %q — BLOQUEADO por guardrail: %s", name, d.Reason))
		return nil
	}
	var err error
	switch act.Kind {
	case KindKey:
		_, err = a.Screen.Key(ctx, act.Combo)
	default:
		_, err = a.Screen.Click(ctx, act.X, act.Y)
	}
	a.view = nil
	return err
}
