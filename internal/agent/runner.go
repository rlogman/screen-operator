package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/llm"
	"screenoperator/internal/screen"
	"screenoperator/internal/summary"
	"screenoperator/internal/tasks"
)

// Runner es el tasks.Runner real: arma un Agent por tarea sobre la sesion que
// abrio el DriverDialer y la Gate de la tarea.
type Runner struct {
	Loader *config.Loader
	Getenv func(string) string
	// NewLLM crea el cliente del modelo (nil = OpenRouter con models.*).
	NewLLM func(m llm.Models) (llm.Chatter, error)
	// NewGuard crea el guardrail de cada tarea (nil = DefaultGuard).
	NewGuard func() Guard
	// GuardFor crea el guardrail de cada tarea con su configuracion y los
	// secretos de la tarea (epica 7). Tiene prioridad sobre NewGuard.
	GuardFor func(cfg *config.Config, secrets []string) Guard
	Now      func() time.Time
}

// Run implementa tasks.Runner.
func (r *Runner) Run(ctx context.Context, job *tasks.Job) tasks.Result {
	failed := func(s string) tasks.Result { return tasks.Result{Status: tasks.StatusFailed, Summary: s} }
	ds, ok := job.Session.(*DriverSession)
	if !ok || ds == nil {
		return failed("No hay conexión con el escritorio.")
	}
	cfg := ds.Config()
	models, err := llm.ModelsFromConfig(cfg)
	if err != nil {
		job.Log.Error("configuracion de modelos invalida", "fase", "agente", "error", err)
		return failed("Error de configuración del agente; la tarea no se inició.")
	}
	var chat llm.Chatter
	if r.NewLLM != nil {
		chat, err = r.NewLLM(models)
	} else {
		var o llm.Options
		o, err = models.ClientOptions(r.getenv)
		if err == nil {
			chat = llm.NewClient(o)
		}
	}
	if err != nil {
		job.Log.Error("no se pudo crear el cliente del modelo", "fase", "agente", "error", err)
		return failed("El agente no tiene acceso al modelo; la tarea no se inició.")
	}
	var b struct {
		MaxSteps int     `json:"max_steps"`
		MaxCost  float64 `json:"max_cost_usd"`
	}
	if err := cfg.Decode("budgets", &b); err != nil {
		return failed("Error de configuración del agente; la tarea no se inició.")
	}
	sumFmt, err := summary.FromConfig(cfg)
	if err != nil {
		return failed("Error de configuración del agente; la tarea no se inició.")
	}
	var todaySrc string
	if v, ok := cfg.Lookup("policy.today_source"); ok {
		todaySrc, _ = v.(string)
	}
	meter := llm.NewMeter(chat, b.MaxCost)
	exec := screen.NewExecutor(gatedDriver{d: ds.Driver(), gate: job.Input}, ds.Surface(), ds.Perception())
	var guard Guard = DefaultGuard{}
	switch {
	case r.GuardFor != nil:
		secrets := make([]string, 0, len(job.Request.DesktopSecrets))
		for _, x := range job.Request.DesktopSecrets {
			secrets = append(secrets, x.Reveal())
		}
		guard = r.GuardFor(cfg, secrets)
	case r.NewGuard != nil:
		guard = r.NewGuard()
	}
	a := &Agent{
		LLM:    meter,
		Screen: exec,
		Guard:  guard,
		Log:    job.Log,
		Now:    r.Now,
		Cfg: Config{
			Actor:      models.Role("actor"),
			Extractor:  models.Role("verifier"),
			MaxSteps:   b.MaxSteps,
			Perception: ds.Perception(),
			Summary:    sumFmt,
			TodayFrom:  todaySrc,
		},
		AppHints: r.appHints,
	}
	if job.Trace != nil {
		a.Trace = job.Trace
	}
	job.Log.Info("agente iniciado", "fase", "agente", "prompt", PromptVersion, "modelo", a.Cfg.Actor.Model,
		"max_pasos", b.MaxSteps, "max_costo_usd", b.MaxCost)
	out := a.Run(ctx, job.Request.Instruction)
	in, outTok, cost, calls := meter.Totals()
	job.Log.Info("agente termino", "fase", "reportar", "terminada", out.Done, "pasos", out.State.Steps,
		"llamadas", calls, "tokens_entrada", in, "tokens_salida", outTok, "costo_usd", cost)
	if out.Done {
		return tasks.Result{Status: tasks.StatusDone, Summary: out.Summary}
	}
	return tasks.Result{Status: tasks.StatusFailed, Summary: out.Summary}
}

func (r *Runner) getenv(k string) string {
	if r.Getenv == nil {
		return ""
	}
	return r.Getenv(k)
}

// appHints resuelve el perfil de app por el texto visible y lo resume para el
// prompt. Con SO_DISABLE_APP_PROFILES (o sin coincidencia unica) no hay pistas.
func (r *Runner) appHints(screenText string) (string, string) {
	if r.Loader == nil {
		return "", ""
	}
	layer, ok, err := r.Loader.ResolveApp(screenText)
	if err != nil || !ok {
		return "", ""
	}
	return layer.ID, RenderHints(layer.Tree)
}

// RenderHints resume un perfil de app (apps/*.yaml) en texto para el prompt.
func RenderHints(tree map[string]any) string {
	app, _ := tree["app"].(map[string]any)
	if app == nil {
		return ""
	}
	var b strings.Builder
	str := func(k string) string { s, _ := app[k].(string); return s }
	if s := str("today_location"); s != "" {
		fmt.Fprintf(&b, "- Fecha de hoy: %s\n", s)
	}
	if s := str("date_format"); s != "" {
		fmt.Fprintf(&b, "- Formato de fecha: %s\n", s)
	}
	if ents, ok := app["entities"].(map[string]any); ok {
		names := make([]string, 0, len(ents))
		for n := range ents {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			e, _ := ents[n].(map[string]any)
			fmt.Fprintf(&b, "- Entidad %s:", n)
			if p, ok := e["id_pattern"].(string); ok {
				fmt.Fprintf(&b, " id %s;", p)
			}
			if f := list(e["fields"]); len(f) > 0 {
				fmt.Fprintf(&b, " campos %s;", strings.Join(f, ", "))
			}
			if s := list(e["states_seen"]); len(s) > 0 {
				fmt.Fprintf(&b, " estados %s;", strings.Join(s, ", "))
			}
			b.WriteString("\n")
		}
	}
	if nav, ok := app["navigation"].([]any); ok {
		for _, x := range nav {
			m, _ := x.(map[string]any)
			g, _ := m["goal"].(string)
			fmt.Fprintf(&b, "- Ir a %s: %s\n", g, strings.Join(list(m["steps"]), " > "))
		}
	}
	for _, q := range list(app["quirks"]) {
		fmt.Fprintf(&b, "- %s\n", q)
	}
	return strings.TrimRight(b.String(), "\n")
}

func list(v any) []string {
	xs, _ := v.([]any)
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
