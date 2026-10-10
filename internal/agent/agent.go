package agent

// Loop por fases (6.3): clasificar, orientar, localizar, actuar, verificar,
// reportar. Las fases son codigo; dentro de localizar/actuar/verificar el
// modelo decide la siguiente accion mirando UNA captura por paso, con el
// historial solo en texto.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"screenoperator/internal/llm"
	"screenoperator/internal/screen"
	"screenoperator/internal/summary"
	"screenoperator/internal/trace"
)

// Intenciones de la clasificacion.
const (
	IntentCreate = "crear"
	IntentEdit   = "editar"
	IntentQuery  = "consultar"
	IntentExport = "exportar"
	IntentReject = "rechazar"
)

// Razones fijas de falla.
const (
	ReasonStepLimit = "límite de pasos"
	ReasonTimeout   = "tiempo agotado antes de terminar"
	ReasonBudget    = "presupuesto agotado"
)

// Screen es lo que el loop usa del executor (screen.Executor lo implementa).
type Screen interface {
	Screenshot(ctx context.Context) (screen.Outcome, error)
	Click(ctx context.Context, x, y int) (screen.Outcome, error)
	DoubleClick(ctx context.Context, x, y int) (screen.Outcome, error)
	Hover(ctx context.Context, x, y int) (screen.Outcome, error)
	TypeReplace(ctx context.Context, x, y int, text string) (screen.Outcome, error)
	Type(ctx context.Context, text string) (screen.Outcome, error)
	Key(ctx context.Context, combo string) (screen.Outcome, error)
	Scroll(ctx context.Context, x, y int, dir string, steps int) (screen.Outcome, error)
	Zoom(ctx context.Context, r image.Rectangle) (*image.RGBA, error)
	EncodeForModel(img *image.RGBA) (screen.ModelShot, []byte, string, error)
}

var _ Screen = (*screen.Executor)(nil)

// Tracer recibe cada paso (lo implementa *trace.Recorder).
type Tracer interface {
	Screenshot(jpeg []byte) error
	Step(s trace.Step) error
}

// Classification es el resultado de la fase clasificar.
type Classification struct {
	Intent       string            `json:"intencion"`
	Entity       string            `json:"entidad"`
	Target       string            `json:"objetivo"`
	Values       map[string]string `json:"-"`
	Question     bool              `json:"pregunta"`
	Asked        []string          `json:"datos_pedidos"`
	Dates        []string          `json:"fechas"`
	Missing      []string          `json:"faltantes"`
	RejectReason string            `json:"razon_rechazo"`
	Plan         string            `json:"plan"`
}

// State es el estado de una tarea, visible para los guardrails.
type State struct {
	Instruction string
	Class       Classification
	Phase       Phase
	Phases      []Phase // fases recorridas, en orden
	Today       time.Time
	TodayFrom   string // "app" o "servidor"
	AppProfile  string // id del perfil de app aplicado ("" = generico)
	Notes       []string
	History     []string
	Steps       int // decisiones del modelo en localizar/actuar/verificar
	Commits     []Action
	Extract     *Aggregator
	// NeedAggregate es pegajoso: se activa con una extraccion de conteo o suma
	// o con una pregunta que pide cantidad, total o suma. Mientras este activo,
	// "terminada" exige un agregado calculado en codigo. NeedAggregateWhy dice
	// que lo activo.
	NeedAggregate    bool
	NeedAggregateWhy string
	// LastCommitStep es el paso del ultimo commit; VerifyFrom el paso en que
	// empezo la ultima fase de verificacion.
	LastCommitStep int
	VerifyFrom     int
	// Escalated lista las decisiones escaladas ("paso:disparador", epica 8).
	Escalated []string
}

func (st *State) enter(p Phase) {
	if st.Phase == p {
		return
	}
	st.Phase = p
	st.Phases = append(st.Phases, p)
}

// Config es la parte de la configuracion que usa el loop.
type Config struct {
	Actor     llm.Role
	Extractor llm.Role
	// Escalation decide los pasos trabados (Model "" = sin escalamiento).
	Escalation       llm.Role
	EscalationPolicy llm.Escalation
	MaxSteps         int
	Perception       screen.Perception
	Summary          summary.Format
	TodayFrom        string // policy.today_source: app o server
}

// Outcome es el resultado de una tarea.
type Outcome struct {
	Done    bool
	Summary string
	Report  summary.Report
	State   *State
}

// Agent ejecuta una tarea. Un Agent por tarea.
type Agent struct {
	LLM    llm.Chatter
	Screen Screen
	Guard  Guard  // nil = DefaultGuard
	Trace  Tracer // nil = sin traza
	Log    *slog.Logger
	Cfg    Config
	Now    func() time.Time
	// AppHints devuelve el id y las pistas del perfil de app que coincide con
	// el texto visible ("" si no hay perfil o estan apagados).
	AppHints func(screenText string) (id, hints string)
	// ReportMargin es cuanto antes del deadline de ctx el loop deja de actuar
	// para reportar con lo que tiene (por defecto 6 s).
	ReportMargin time.Duration
	// StableRetries es cuantas veces se vuelve a capturar una pantalla no
	// estable antes de consumir un paso esperando (por defecto 2).
	StableRetries int

	st           *State
	hints        string
	view         *View // imagen actual del modelo (nil = capturar de nuevo)
	full         *screen.ModelShot
	stable       bool     // la vista actual salio de una captura estable
	lastNoEffect string   // ultima accion que no cambio la pantalla
	recent       []string // ultimas acciones de pantalla
	verifyNudged bool     // ya se rechazo una vez un "fallida" sin intentar verificar
	// nextXY: centro de "siguiente" en pantalla segun la ultima extraccion.
	nextX, nextY int
	hasNextXY    bool
	deadline     time.Time
	parseErrs    int
	esc          escalator
}

func (a *Agent) init() {
	if a.Guard == nil {
		a.Guard = DefaultGuard{}
	}
	if a.Log == nil {
		a.Log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.ReportMargin <= 0 {
		a.ReportMargin = 6 * time.Second
	}
	if a.StableRetries <= 0 {
		a.StableRetries = 2
	}
	if a.Cfg.MaxSteps <= 0 {
		a.Cfg.MaxSteps = 40
	}
	if a.Cfg.Extractor.Model == "" {
		a.Cfg.Extractor = a.Cfg.Actor
	}
}

func (a *Agent) logInfo(msg string, args ...any) {
	a.Log.Info(msg, append([]any{"fase", string(a.st.Phase)}, args...)...)
}

// Run ejecuta la orden y devuelve el resultado. Nunca entra en panico por
// salidas del modelo; respeta ctx.
func (a *Agent) Run(ctx context.Context, instruction string) Outcome {
	a.init()
	a.st = &State{Instruction: strings.TrimSpace(instruction)}
	st := a.st
	if dl, ok := ctx.Deadline(); ok {
		a.deadline = dl.Add(-a.ReportMargin)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, a.deadline)
		defer cancel()
	}

	// 1. Clasificar (con la captura inicial; no envia eventos).
	st.enter(PhaseClassify)
	if err := a.capture(ctx); err != nil {
		return a.fail(ctx, "no se pudo capturar la pantalla inicial: "+errText(err), nil)
	}
	if err := a.classify(ctx); err != nil {
		return a.failErr(ctx, "no se pudo clasificar la orden", err)
	}
	if st.Class.Question && AsksQuantity(st.Instruction, st.Class.Asked) {
		st.NeedAggregate, st.NeedAggregateWhy = true, "la orden pide una cantidad, total o suma"
	}
	if d := a.Guard.Classified(st); d.Deny {
		return a.fail(ctx, d.Reason, d.Identifiers)
	}

	// 2. Orientar: fecha de la app, titulo, perfil de app.
	st.enter(PhaseOrient)
	if err := a.orient(ctx); err != nil {
		return a.failErr(ctx, "no se pudo leer la pantalla inicial", err)
	}

	// 3-5. Localizar, actuar, verificar.
	st.enter(PhaseLocate)
	return a.loop(ctx)
}

func (a *Agent) loop(ctx context.Context) Outcome {
	st := a.st
	modelErrs := 0
	finishRejects := 0
	var hint string
	for st.Steps < a.Cfg.MaxSteps {
		if ctx.Err() != nil || (!a.deadline.IsZero() && time.Until(a.deadline) < time.Second) {
			return a.fail(ctx, ReasonTimeout, nil)
		}
		if a.view == nil || !a.stable {
			stable, err := a.stableCapture(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return a.fail(ctx, ReasonTimeout, nil)
				}
				return a.fail(ctx, "no se pudo capturar la pantalla: "+errText(err), nil)
			}
			if !stable {
				// Carryover epica 2: no se decide sobre una captura no estable.
				st.Steps++
				a.record(fmt.Sprintf("%d. [%s] (esperar) la pantalla no se estabilizo; se vuelve a capturar", st.Steps, st.Phase))
				a.traceStep(trace.Step{Phase: string(st.Phase), Action: "esperar estabilidad", Reason: "captura no estable"}, llm.Usage{}, 0)
				a.view = nil
				continue
			}
		}
		st.Steps++
		startStep := a.Now()
		phaseBefore := st.Phase
		act, usage, err := a.decide(ctx, hint)
		hint = ""
		if err != nil {
			a.traceStep(trace.Step{Phase: string(st.Phase), Action: "error", Reason: errText(err)}, usage, a.Now().Sub(startStep))
			switch {
			case errors.Is(err, llm.ErrBudget):
				return a.fail(ctx, ReasonBudget, nil)
			case ctx.Err() != nil:
				return a.fail(ctx, ReasonTimeout, nil)
			}
			var pe *ParseError
			if errors.As(err, &pe) {
				a.record(fmt.Sprintf("%d. [%s] respuesta invalida del modelo (%s); el paso cuenta como error", st.Steps, st.Phase, pe.Error()))
				continue
			}
			modelErrs++
			a.record(fmt.Sprintf("%d. [%s] error al consultar el modelo", st.Steps, st.Phase))
			if modelErrs >= 3 {
				return a.fail(ctx, "el modelo no respondio: "+errText(err), nil)
			}
			continue
		}
		modelErrs = 0
		if act.Kind != KindFinish && act.Phase != "" && phaseBefore.Before(act.Phase) {
			st.enter(act.Phase)
			if act.Phase == PhaseVerify {
				st.VerifyFrom = st.Steps
			}
		}
		if act.Note != "" {
			a.addNote(act.Note)
		}
		a.traceStep(trace.Step{Phase: string(st.Phase), Action: act.String(), Reason: act.Reason}, usage, a.Now().Sub(startStep))

		switch act.Kind {
		case KindZoom:
			a.doZoom(ctx, act)
		case KindExtract:
			if err := a.doExtract(ctx, act); err != nil {
				if errors.Is(err, llm.ErrBudget) {
					return a.fail(ctx, ReasonBudget, nil)
				}
				if ctx.Err() != nil {
					return a.fail(ctx, ReasonTimeout, nil)
				}
				a.record(fmt.Sprintf("%d. [%s] %s — la extraccion fallo: %s", st.Steps, st.Phase, act, errText(err)))
			}
		case KindFinish:
			out, done, h := a.doFinish(ctx, act, phaseBefore, &finishRejects)
			if done {
				return out
			}
			hint = h
		default:
			if err := a.doScreenAction(ctx, act); err != nil {
				if ctx.Err() != nil {
					return a.fail(ctx, ReasonTimeout, nil)
				}
				a.record(fmt.Sprintf("%d. [%s] %s — error al ejecutar: %s", st.Steps, st.Phase, act, errText(err)))
				a.view = nil
			}
		}
	}
	return a.fail(ctx, fmt.Sprintf("%s (%d) alcanzado sin terminar la tarea", ReasonStepLimit, a.Cfg.MaxSteps), nil)
}

// capture toma una captura (estable o no) y la deja como vista actual.
func (a *Agent) capture(ctx context.Context) error {
	_, err := a.stableCapture(ctx)
	return err
}

// stableCapture captura esperando estabilidad, reintentando hasta
// StableRetries veces. Deja la vista actual aunque no sea estable.
func (a *Agent) stableCapture(ctx context.Context) (bool, error) {
	var out screen.Outcome
	var err error
	for i := 0; i <= a.StableRetries; i++ {
		out, err = a.Screen.Screenshot(ctx)
		if err != nil {
			return false, err
		}
		if out.Stable {
			break
		}
	}
	a.setFull(out.Screen)
	a.stable = out.Stable
	return out.Stable, nil
}

func (a *Agent) setFull(img *image.RGBA) {
	shot := screen.ForModel(img, a.Cfg.Perception.Screenshot.MaxWidth)
	a.full = &shot
	v := FullView(shot)
	a.view = &v
}

// userMessage arma el mensaje del paso: contexto en texto + exactamente una imagen.
func (a *Agent) userMessage(task string, extra string, v View) (llm.Message, []byte, error) {
	st := a.st
	b, mime, err := a.Cfg.Perception.Encode(v.Img)
	if err != nil {
		return llm.Message{}, nil, err
	}
	w, h := v.Size()
	desc := fmt.Sprintf("captura completa de la pantalla, %dx%d (coordenadas = pixeles de esta imagen)", w, h)
	if v.Zoomed {
		r := image.Rect(v.Origin.X, v.Origin.Y, v.Origin.X+int(float64(w)*v.ScaleX+0.5), v.Origin.Y+int(float64(h)*v.ScaleY+0.5))
		desc = fmt.Sprintf("ZOOM de la region de pantalla (%d,%d)-(%d,%d), ampliada a %dx%d (coordenadas = pixeles de esta imagen ampliada)",
			r.Min.X, r.Min.Y, r.Max.X, r.Max.Y, w, h)
	}
	today := "(todavia no leida)"
	if !st.Today.IsZero() {
		today = fmt.Sprintf("%s (%s, fuente: %s)", st.Today.Format("2006-01-02"), WeekdayES(st.Today), st.TodayFrom)
	}
	secs := 0
	if !a.deadline.IsZero() {
		secs = int(time.Until(a.deadline).Seconds())
	}
	d := stepData{
		Instruction: st.Instruction,
		Intent:      orDash(st.Class.Intent),
		Entity:      st.Class.Entity,
		Target:      st.Class.Target,
		Values:      kv(st.Class.Values),
		Asked:       strings.Join(st.Class.Asked, "; "),
		Plan:        orDash(st.Class.Plan),
		Today:       today,
		Phase:       st.Phase,
		Preface:     st.Phase == PhaseClassify || st.Phase == PhaseOrient,
		Hints:       a.hints,
		Notes:       st.Notes,
		History:     lastN(st.History, 30),
		StepsLeft:   a.Cfg.MaxSteps - st.Steps,
		SecondsLeft: secs,
		ImageDesc:   desc,
	}
	if st.Extract != nil {
		d.Extraction = st.Extract.Summary()
	}
	d.Instruction2 = extra
	text := render("context", d) + "\n\n" + task
	return llm.Message{Role: "user", Parts: []llm.Part{llm.TextPart(text), llm.ImagePart(b, mime)}}, b, nil
}

// ask hace una llamada con una imagen y valida la salida con parse; ante una
// salida invalida pide una reparacion una vez. Devuelve el uso sumado.
func (a *Agent) ask(ctx context.Context, purpose string, role llm.Role, user llm.Message, shot []byte, parse func(string) error) (llm.Usage, error) {
	if a.Trace != nil && shot != nil {
		_ = a.Trace.Screenshot(shot)
	}
	sysText := SystemPrompt()
	if purpose == "extraer" {
		sysText = ExtractSystemPrompt()
	}
	sys := llm.Message{Role: "system", Parts: []llm.Part{llm.TextPart(sysText)}}
	req := llm.Request{Model: role.Model, Temperature: role.Temperature, MaxTokens: role.MaxTokens, JSON: true, Purpose: purpose,
		ReasoningEffort: role.Reasoning, Messages: []llm.Message{sys, user}}
	var total llm.Usage
	resp, err := a.LLM.Chat(ctx, req)
	total = addUsage(total, resp.Usage)
	if err != nil {
		return total, err
	}
	perr := parse(resp.Content)
	if perr == nil {
		return total, nil
	}
	a.logInfo("salida invalida del modelo; se pide reparacion", "proposito", purpose, "error", perr.Error(),
		"fin", resp.FinishReason, "tokens_salida", resp.Usage.CompletionTokens, "tokens_razonamiento", resp.Usage.ReasoningTokens, "contenido", truncate(resp.Content, 400))
	req.Purpose = purpose + " (reparacion)"
	req.Messages = []llm.Message{sys, user,
		{Role: "assistant", Parts: []llm.Part{llm.TextPart(truncate(resp.Content, 2000))}},
		{Role: "user", Parts: []llm.Part{llm.TextPart(render("repair", perr.Error()))}}}
	resp, err = a.LLM.Chat(ctx, req)
	total = addUsage(total, resp.Usage)
	if err != nil {
		return total, err
	}
	if err := parse(resp.Content); err != nil {
		a.logInfo("salida invalida tras la reparacion", "proposito", purpose, "error", err.Error(),
			"fin", resp.FinishReason, "tokens_salida", resp.Usage.CompletionTokens, "tokens_razonamiento", resp.Usage.ReasoningTokens, "contenido", truncate(resp.Content, 400))
		return total, err
	}
	return total, nil
}

func addUsage(a, b llm.Usage) llm.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.CostUSD += b.CostUSD
	return a
}

// decide pide la siguiente accion al modelo.
func (a *Agent) decide(ctx context.Context, hint string) (Action, llm.Usage, error) {
	v := *a.view
	msg, shot, err := a.userMessage(render("act", nil), hint, v)
	if err != nil {
		return Action{}, llm.Usage{}, err
	}
	var act Action
	u, err := a.ask(ctx, "actuar", a.actorRole(), msg, shot, func(s string) error {
		var perr error
		act, perr = Parse(s, v)
		return perr
	})
	return act, u, err
}

// classify corre la fase clasificar.
func (a *Agent) classify(ctx context.Context) error {
	start := a.Now()
	msg, shot, err := a.userMessage(render("classify", nil), "", *a.view)
	if err != nil {
		return err
	}
	var c Classification
	u, err := a.ask(ctx, "clasificar", a.Cfg.Actor, msg, shot, func(s string) error {
		var perr error
		c, perr = parseClassification(s)
		return perr
	})
	if err != nil {
		a.traceStep(trace.Step{Phase: string(PhaseClassify), Action: "clasificar", Reason: "error: " + errText(err)}, u, a.Now().Sub(start))
		return err
	}
	a.st.Class = c
	a.traceStep(trace.Step{Phase: string(PhaseClassify), Action: "clasificar: " + c.Intent,
		Reason: fmt.Sprintf("entidad=%s objetivo=%s pregunta=%v plan=%s", c.Entity, c.Target, c.Question, c.Plan)}, u, a.Now().Sub(start))
	a.logInfo("orden clasificada", "intencion", c.Intent, "entidad", c.Entity, "pregunta", c.Question)
	return nil
}

func parseClassification(s string) (Classification, error) {
	var raw struct {
		Classification
		Values map[string]any `json:"valores"`
	}
	dec := json.NewDecoder(strings.NewReader(llm.StripFences(s)))
	if err := dec.Decode(&raw); err != nil {
		return Classification{}, &ParseError{Msg: "no es un objeto JSON: " + shorten(err.Error())}
	}
	c := raw.Classification
	c.Intent = strings.ToLower(strings.TrimSpace(c.Intent))
	switch c.Intent {
	case IntentCreate, IntentEdit, IntentQuery, IntentExport, IntentReject:
	case "":
		return c, perr("intencion", "falta")
	default:
		return c, perr("intencion", "%q invalida (crear, editar, consultar, exportar, rechazar)", c.Intent)
	}
	if len(raw.Values) > 0 {
		c.Values = map[string]string{}
		for k, v := range raw.Values {
			if v != nil {
				c.Values[k] = strings.TrimSpace(fmt.Sprint(v))
			}
		}
	}
	if c.Intent == IntentQuery {
		c.Question = true
	}
	return c, nil
}

// orient corre la fase orientar: fecha de la app, titulo y perfil.
func (a *Agent) orient(ctx context.Context) error {
	st := a.st
	start := a.Now()
	msg, shot, err := a.userMessage(render("orient", nil), "", *a.view)
	if err != nil {
		return err
	}
	var o struct {
		Hoy     *string  `json:"hoy"`
		Formato string   `json:"formato_fecha"`
		Titulo  string   `json:"titulo"`
		Texto   string   `json:"texto_visible"`
		Menus   []string `json:"menus"`
	}
	u, err := a.ask(ctx, "orientar", a.Cfg.Actor, msg, shot, func(s string) error {
		if err := json.Unmarshal([]byte(llm.StripFences(s)), &o); err != nil {
			return &ParseError{Msg: "no es un objeto JSON: " + shorten(err.Error())}
		}
		return nil
	})
	if err != nil {
		a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "orientar", Reason: "error: " + errText(err)}, u, a.Now().Sub(start))
		return err
	}
	var note string
	if a.Cfg.TodayFrom != "server" && o.Hoy != nil && strings.TrimSpace(*o.Hoy) != "" {
		if t, perr := ParseAppDate(*o.Hoy, o.Formato); perr == nil {
			st.Today, st.TodayFrom = t, "app"
			note = fmt.Sprintf("fecha de la app: %s (%s)", t.Format("2006-01-02"), WeekdayES(t))
		} else {
			note = fmt.Sprintf("la fecha leida %q no se pudo interpretar; se usa la fecha del servidor", *o.Hoy)
		}
	}
	if st.Today.IsZero() {
		st.Today, st.TodayFrom = dayOf(a.Now()), "servidor"
		if note == "" {
			note = "la app no muestra la fecha de hoy; se usa la fecha del servidor"
			if a.Cfg.TodayFrom == "server" {
				note = "policy.today_source=server: se usa la fecha del servidor"
			}
		}
		note += fmt.Sprintf(": %s (%s)", st.Today.Format("2006-01-02"), WeekdayES(st.Today))
	}
	a.addNote(note)
	for _, expr := range st.Class.Dates {
		if r, ok := ResolveRelative(st.Today, expr); ok {
			a.addNote(fmt.Sprintf("%q = %s", expr, r))
		}
	}
	screenText := strings.Join(append([]string{o.Titulo, o.Texto}, o.Menus...), "\n")
	if a.AppHints != nil {
		if id, h := a.AppHints(screenText); id != "" {
			st.AppProfile, a.hints = id, h
		}
	}
	reason := note
	if st.AppProfile != "" {
		reason += "; perfil de app: " + st.AppProfile
	}
	a.traceStep(trace.Step{Phase: string(PhaseOrient), Action: "orientar: " + truncate(o.Titulo, 80), Reason: reason}, u, a.Now().Sub(start))
	a.logInfo("orientacion", "hoy", st.Today.Format("2006-01-02"), "fuente", st.TodayFrom, "perfil_app", st.AppProfile)
	return nil
}

func (a *Agent) doZoom(ctx context.Context, act Action) {
	st := a.st
	if a.view.Zoomed {
		a.record(fmt.Sprintf("%d. [%s] %s — rechazado: maximo un zoom seguido; actua sobre el zoom o la captura", st.Steps, st.Phase, act))
		a.view = nil
		return
	}
	if !a.Cfg.Perception.Zoom.Enabled || a.Cfg.Perception.Zoom.MaxPerStep == 0 {
		a.record(fmt.Sprintf("%d. [%s] %s — rechazado: zoom deshabilitado", st.Steps, st.Phase, act))
		return
	}
	img, err := a.Screen.Zoom(ctx, act.Region)
	if err != nil {
		a.record(fmt.Sprintf("%d. [%s] %s — no se pudo ampliar: %s", st.Steps, st.Phase, act, errText(err)))
		return
	}
	v := ZoomView(img, act.Region, a.full.ScreenW, a.full.ScreenH)
	a.view = &v
	a.record(fmt.Sprintf("%d. [%s] %s — el siguiente mensaje trae el zoom", st.Steps, st.Phase, act))
}

// doExtract lee las filas visibles con el extractor y las acumula en codigo.
func (a *Agent) doExtract(ctx context.Context, act Action) error {
	st := a.st
	if a.full == nil {
		return errors.New("sin captura")
	}
	full := FullView(*a.full)
	filters := ""
	if len(act.Filters) > 0 {
		filters = strings.Join(sortedMap(act.Filters), ", ")
	}
	msg, shot, err := a.userMessage(render("extract", extractData{What: act.What, Key: act.KeyCol, Filters: filters}), "", full)
	if err != nil {
		return err
	}
	start := a.Now()
	var page Page
	u, err := a.ask(ctx, "extraer", a.Cfg.Extractor, msg, shot, func(s string) error {
		var perr error
		page, perr = ParsePage(s)
		return perr
	})
	a.traceStep(trace.Step{Phase: string(st.Phase), Action: fmt.Sprintf("extraer filas (%d)", len(page.Rows)), Reason: act.What}, u, a.Now().Sub(start))
	if err != nil {
		return err
	}
	if (act.Purpose == "conteo" || act.Purpose == "suma") && !st.NeedAggregate {
		st.NeedAggregate, st.NeedAggregateWhy = true, "extrajiste con purpose \""+act.Purpose+"\""
	}
	if st.Extract == nil || !st.Extract.Same(act.KeyCol, act.Purpose, act.Filters) {
		st.Extract = NewAggregator(act.What, act.KeyCol, act.Purpose, act.Filters)
	}
	added, _ := st.Extract.AddPage(page)
	a.hasNextXY = false
	msgStop := st.Extract.Stop
	if st.Extract.Complete {
		msgStop = "extraccion completa (" + st.Extract.Stop + ")"
	} else if st.Extract.Stopped {
		msgStop = "extraccion INCOMPLETA: " + st.Extract.Stop
	}
	if page.HasNextXY && page.NextEnabled && !st.Extract.Complete {
		if page.NextX >= 0 && page.NextY >= 0 && page.NextX < full.Img.Bounds().Dx() && page.NextY < full.Img.Bounds().Dy() {
			x, y := full.ToScreen(page.NextX, page.NextY)
			a.nextX, a.nextY, a.hasNextXY = x, y, true
			msgStop += fmt.Sprintf(" (el lector ubico 'siguiente' en %d,%d; un solo clic por pagina)", x, y)
		}
	}
	if page.Indicator != "" {
		msgStop += "; indicador: " + truncate(page.Indicator, 40)
	}
	a.record(fmt.Sprintf("%d. [%s] %s — %d filas visibles, %d nuevas; acumulado %d unicas, %d cumplen filtros; %s",
		st.Steps, st.Phase, act, len(page.Rows), added, len(st.Extract.Rows()), st.Extract.Count(), msgStop))
	return nil
}

// doScreenAction ejecuta una accion que envia eventos, previo guardrail.
func (a *Agent) doScreenAction(ctx context.Context, act Action) error {
	st := a.st
	if d := a.Guard.BeforeAction(st, act); d.Deny {
		a.record(fmt.Sprintf("%d. [%s] %s — BLOQUEADO por guardrail: %s", st.Steps, st.Phase, act, d.Reason))
		return nil
	}
	var out screen.Outcome
	var err error
	switch act.Kind {
	case KindClick, KindCommit:
		out, err = a.Screen.Click(ctx, act.X, act.Y)
	case KindDoubleClick:
		out, err = a.Screen.DoubleClick(ctx, act.X, act.Y)
	case KindHover:
		out, err = a.Screen.Hover(ctx, act.X, act.Y)
	case KindType:
		if act.Replace {
			out, err = a.Screen.TypeReplace(ctx, act.X, act.Y, act.Text)
		} else {
			out, err = a.Screen.Type(ctx, act.Text)
		}
	case KindKey:
		out, err = a.Screen.Key(ctx, act.Combo)
	case KindScroll:
		out, err = a.Screen.Scroll(ctx, act.X, act.Y, act.Dir, act.Amount)
	default:
		return fmt.Errorf("accion %s sin ejecutor", act.Kind)
	}
	if act.Kind == KindCommit {
		st.Commits = append(st.Commits, act)
		st.LastCommitStep = st.Steps
	}
	if err != nil {
		return err
	}
	result := "sin cambio visible"
	if out.Changed {
		result = "la pantalla cambio"
	}
	if !out.Stable {
		result += "; la pantalla seguia cambiando (se vuelve a capturar)"
	}
	if act.Kind == KindCommit && !act.ConfirmDialog {
		result += "; si aparecio un dialogo de confirmacion, leelo: confirmalo solo si describe exactamente la accion pedida"
	}
	reason := ""
	if act.Reason != "" {
		reason = " (" + truncate(act.Reason, 120) + ")"
	}
	if !out.Changed && act.String() == a.lastNoEffect {
		result += "; ES LA SEGUNDA VEZ SIN EFECTO: no repitas este punto; pide zoom para ubicar el elemento o prueba otro camino"
		a.escSignal(llm.TriggerNoEffect)
	}
	if out.Changed {
		a.lastNoEffect = ""
	} else {
		a.lastNoEffect = act.String()
	}
	pageAdv := out.Changed && st.Extract != nil && a.isPageAdvance(act)
	if pageAdv {
		st.Extract.NoteAdvance()
		if !st.Extract.Complete {
			result += "; si la lista cambio de pagina, EXTRAE esta pagina ahora, antes de volver a avanzar"
		}
	}
	a.recent = append(a.recent, act.String())
	if len(a.recent) > 4 {
		a.recent = a.recent[len(a.recent)-4:]
	}
	if n := countEqual(a.recent, act.String()); n >= 3 && out.Changed && !pageAdv {
		// Un clic que abre y cierra lo mismo (p. ej. un menu) cambia la pantalla
		// pero no avanza.
		result += fmt.Sprintf("; REPETISTE ESTA ACCION %d VECES SIN AVANZAR: un clic sobre un menu abierto lo cierra; usa hover sobre la opcion o el submenu, pide zoom o prueba otro camino", n)
		a.escSignal(llm.TriggerNoEffect)
	}
	a.record(fmt.Sprintf("%d. [%s] %s%s → %s", st.Steps, st.Phase, act, reason, result))
	if out.Stable && out.Screen != nil {
		a.setFull(out.Screen)
		a.stable = true
	} else {
		a.view = nil // carryover epica 2: recapturar antes de decidir
	}
	return nil
}

// doFinish procesa una solicitud de cierre. Devuelve done=true con el
// resultado final, o un hint para el paso siguiente.
func (a *Agent) doFinish(ctx context.Context, act Action, phaseBefore Phase, rejects *int) (Outcome, bool, string) {
	st := a.st
	f := act.Finish
	if f.Status == StatusFailed && writes(st.Class.Intent) && len(st.Commits) > 0 && st.Phase == PhaseVerify &&
		st.Steps-st.VerifyFrom <= 1 && !a.verifyNudged {
		// Tras un commit, "fallida" sin haber intentado verificar puede negar un
		// cambio que si quedo: una sola vez se pide verificar antes de decidir.
		a.verifyNudged = true
		return Outcome{}, false, a.rejectFinish(rejects, act, "todavia no verificaste: busca o reabre el registro y lee el valor guardado; recien entonces decide terminada (verificado) o fallida")
	}
	if f.Status == StatusFailed {
		a.record(fmt.Sprintf("%d. [%s] %s — el modelo pide cerrar como fallida: %s", st.Steps, st.Phase, act, f.Reason))
		d := a.Guard.BeforeFinish(st, f)
		reason, ids := f.Reason, f.Identifiers
		if d.Deny {
			reason, ids = d.Reason, append(ids, d.Identifiers...)
		}
		return a.final(ctx, false, reason, ids, f), true, ""
	}
	// terminada: la verificacion es obligatoria y posterior al ultimo commit.
	needVerify := phaseBefore != PhaseVerify || (st.LastCommitStep > 0 && st.LastCommitStep >= st.VerifyFrom)
	if needVerify {
		*rejects++
		if *rejects > 3 {
			return a.fail(ctx, "no se pudo verificar el resultado antes de terminar", nil), true, ""
		}
		st.enter(PhaseVerify)
		st.VerifyFrom = st.Steps
		a.record(fmt.Sprintf("%d. [%s] %s — no aceptado todavia: falta verificar", st.Steps, st.Phase, act))
		a.view = nil // verificar sobre una captura nueva
		return Outcome{}, false, verifyHint(st)
	}
	if f.Aggregate == "" && st.NeedAggregate {
		// Un conteo o una suma nunca se acepta "a ojo": el numero lo pone el
		// codigo. El flag es pegajoso: cambiar de purpose o no extraer no lo apaga.
		if *rejects > 3 {
			return a.fail(ctx, "la respuesta (conteo o suma) no vino calculada por el codigo sobre todas las paginas", nil), true, ""
		}
		return Outcome{}, false, a.rejectFinish(rejects, act, st.NeedAggregateWhy+": extrae la lista con extract en cada pagina y termina con \"agregado\":\"conteo\" (o \"suma:<columna>\") para que el codigo calcule el numero; no lo pongas a ojo en datos")
	}
	if f.Aggregate != "" {
		if st.Extract == nil || st.Extract.Pages() == 0 {
			return Outcome{}, false, a.rejectFinish(rejects, act, "pediste agregado pero no hay filas extraidas: usa extract en cada pagina")
		}
		if !st.Extract.Complete {
			if *rejects > 3 {
				return a.fail(ctx, "la extraccion de la lista quedo incompleta: "+st.Extract.Stop, nil), true, ""
			}
			return Outcome{}, false, a.rejectFinish(rejects, act, "la extraccion no esta completa: "+st.Extract.Stop)
		}
		val, err := a.aggregate(f.Aggregate)
		if err != nil {
			return a.fail(ctx, "no se pudo calcular la respuesta: "+err.Error(), nil), true, ""
		}
		f.Data = append([]string{val}, dropFirst(f.Data)...)
	}
	if writes(st.Class.Intent) && !f.Verified {
		return a.final(ctx, false, "no se pudo verificar que el cambio quedó guardado: "+firstNonEmpty(f.Evidence, f.Reason), f.Identifiers, f), true, ""
	}
	if d := a.Guard.BeforeFinish(st, f); d.Deny {
		return a.final(ctx, false, d.Reason, d.Identifiers, f), true, ""
	}
	a.record(fmt.Sprintf("%d. [%s] %s — aceptado", st.Steps, st.Phase, act))
	return a.final(ctx, true, f.Reason, nil, f), true, ""
}

// isPageAdvance reconoce un clic que avanza de pagina: cerca del
// "siguiente" que ubico el lector, o descrito como tal en la razon.
func (a *Agent) isPageAdvance(act Action) bool {
	switch act.Kind {
	case KindClick, KindDoubleClick:
	default:
		return false
	}
	if a.hasNextXY {
		dx, dy := act.X-a.nextX, act.Y-a.nextY
		if dx*dx+dy*dy <= 40*40 {
			return true
		}
	}
	r := strings.ToLower(act.Reason)
	for _, w := range []string{"siguiente", "next", "proxima", "próxima", "avanza"} {
		if strings.Contains(r, w) {
			return true
		}
	}
	return false
}

func (a *Agent) rejectFinish(rejects *int, act Action, why string) string {
	*rejects++
	a.escSignal(llm.TriggerFinishRejected)
	a.record(fmt.Sprintf("%d. [%s] %s — no aceptado: %s", a.st.Steps, a.st.Phase, act, why))
	return why
}

func verifyHint(st *State) string {
	if writes(st.Class.Intent) {
		return "Antes de terminar, VERIFICA: vuelve a abrir o buscar el registro (o el listado de reportes) y lee el valor guardado. Luego responde finish con \"verificado\":true y la evidencia, o \"fallida\" si no coincide."
	}
	return "Antes de terminar, VERIFICA que los datos de tu respuesta se leen en pantalla (o que la extraccion esta completa) y responde finish de nuevo."
}

func writes(intent string) bool {
	return intent == IntentCreate || intent == IntentEdit || intent == IntentExport
}

func (a *Agent) aggregate(spec string) (string, error) {
	g := a.st.Extract
	if spec == "conteo" {
		return fmt.Sprint(g.Count()), nil
	}
	col := strings.TrimSpace(strings.TrimPrefix(spec, "suma:"))
	s, err := g.Sum(col)
	if err != nil {
		return "", err
	}
	return FormatNumber(s), nil
}

func dropFirst(xs []string) []string {
	if len(xs) <= 1 {
		return nil
	}
	return xs[1:]
}

// fail termina la tarea como fallida con una razon del loop.
func (a *Agent) fail(ctx context.Context, reason string, ids []string) Outcome {
	return a.final(ctx, false, reason, ids, nil)
}

func (a *Agent) failErr(ctx context.Context, what string, err error) Outcome {
	switch {
	case errors.Is(err, llm.ErrBudget):
		return a.fail(ctx, ReasonBudget, nil)
	case ctx.Err() != nil:
		return a.fail(ctx, ReasonTimeout, nil)
	}
	return a.fail(ctx, what+": "+errText(err), nil)
}

// final arma el resumen (fase reportar).
func (a *Agent) final(_ context.Context, done bool, reason string, ids []string, f *FinishRequest) Outcome {
	st := a.st
	st.enter(PhaseReport)
	r := summary.Report{
		Done:     done,
		Intent:   st.Class.Intent,
		Entity:   st.Class.Entity,
		Target:   st.Class.Target,
		Question: st.Class.Question,
		Reason:   reason,
		Steps:    st.Steps,
	}
	if f != nil {
		if f.Entity != "" {
			r.Entity = f.Entity
		}
		r.EntityID = f.EntityID
		if done {
			r.Changed = f.Changed
			r.Answer = f.Data
			if f.Evidence != "" {
				r.Notes = append(r.Notes, "Verificación: "+strings.TrimRight(f.Evidence, ". ")+".")
			}
		}
		ids = append(append([]string(nil), f.Identifiers...), ids...)
	}
	r.Identifiers = uniq(ids)
	if done && f != nil && f.Aggregate != "" && st.Extract != nil && st.Extract.Pages() > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("Conteo en código sobre %d páginas extraídas (%d filas únicas).", st.Extract.Pages(), len(st.Extract.Rows())))
	}
	s := a.Cfg.Summary.Build(r)
	status := StatusFailed
	if done {
		status = StatusDone
	}
	a.traceStep(trace.Step{Phase: string(PhaseReport), Action: "reportar " + status, Reason: reason}, llm.Usage{}, 0)
	a.Log.Info("tarea terminada por el agente", "fase", string(PhaseReport), "estado", status, "pasos", st.Steps,
		"fases", fmt.Sprint(st.Phases), "escalados", fmt.Sprint(st.Escalated))
	return Outcome{Done: done, Summary: s, Report: r, State: st}
}

func (a *Agent) traceStep(s trace.Step, u llm.Usage, d time.Duration) {
	s.TokensIn, s.TokensOut, s.CostUSD, s.Duration = u.PromptTokens, u.CompletionTokens, u.CostUSD, d
	a.esc.spent += u.CostUSD
	if a.Trace != nil {
		_ = a.Trace.Step(s)
	}
}

func (a *Agent) record(line string) {
	a.st.History = append(a.st.History, line)
	a.logInfo("paso", "historial", line)
}

func (a *Agent) addNote(n string) {
	n = strings.TrimSpace(n)
	if n == "" {
		return
	}
	for _, x := range a.st.Notes {
		if x == n {
			return
		}
	}
	a.st.Notes = append(a.st.Notes, truncate(n, 300))
	if len(a.st.Notes) > 15 {
		a.st.Notes = a.st.Notes[len(a.st.Notes)-15:]
	}
}

func lastN(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	out := append([]string{fmt.Sprintf("(... %d acciones anteriores omitidas)", len(xs)-n)}, xs[len(xs)-n:]...)
	return out
}

func kv(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, ", ")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return truncate(err.Error(), 200)
}

func countEqual(xs []string, s string) int {
	n := 0
	for _, x := range xs {
		if x == s {
			n++
		}
	}
	return n
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
