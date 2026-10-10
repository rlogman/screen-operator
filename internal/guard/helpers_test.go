package guard

import (
	"context"
	"fmt"
	"image/color"
	"sync"
	"testing"
	"time"

	"screenoperator/internal/agent"
	"screenoperator/internal/config"
	"screenoperator/internal/fakes"
	"screenoperator/internal/llm"
	"screenoperator/internal/screen"
	"screenoperator/internal/summary"
)

// routerLLM responde las lecturas cortas del guardrail (etiqueta, dialogo)
// por proposito y el resto en orden de guion.
type routerLLM struct {
	mu     sync.Mutex
	script []string
	label  []string // respuestas de "etiqueta", en orden
	dialog []string // respuestas de "dialogo", en orden ({"dialogo":false} si se acaban)
	calls  []llm.Request
}

func (r *routerLLM) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	pop := func(xs *[]string, def string) string {
		if len(*xs) == 0 {
			return def
		}
		s := (*xs)[0]
		*xs = (*xs)[1:]
		return s
	}
	var c string
	switch req.Purpose {
	case "etiqueta":
		c = pop(&r.label, `{"texto":""}`)
	case "dialogo":
		c = pop(&r.dialog, `{"dialogo":false,"texto":""}`)
	default:
		if len(r.script) == 0 {
			return llm.Response{}, fmt.Errorf("guion agotado (%s)", req.Purpose)
		}
		c = pop(&r.script, "")
	}
	return llm.Response{Content: c, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5}}, nil
}

func fastSurface() (screen.Surface, screen.Perception) {
	var s screen.Surface
	s.ID, s.Transport = "test", "rfb"
	s.Screen.Width, s.Screen.Height = 1280, 800
	s.Input.ClickHoldMS = 1
	s.Input.ReplaceText.Strategy = "select_all_then_type"
	s.Input.ReplaceText.Chord = []string{"ctrl", "a"}
	s.Input.Scroll.WheelClicksPerStep = 1
	s.Settle = screen.SettleConfig{MinWaitMS: 0, MaxWaitMS: 60, PollMS: 10, StableFrames: 1, DiffThreshold: 0.002}
	var p screen.Perception
	p.Screenshot.Format, p.Screenshot.JPEGQuality, p.Screenshot.MaxWidth = "jpeg", 60, 1280
	p.Zoom.Enabled, p.Zoom.Factor, p.Zoom.MaxPerStep = true, 2, 1
	return s, p
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.ForTask("wss://x/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// newAgent arma un Agent real con el guardrail de la configuracion base.
func newAgent(t *testing.T, chat llm.Chatter, d *fakes.Driver, secrets ...string) (*agent.Agent, *Guard) {
	t.Helper()
	cfg := testConfig(t)
	f, err := summary.FromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g := ForTask(func(string) string { return "" })(cfg, secrets).(*Guard)
	s, p := fastSurface()
	a := &agent.Agent{
		LLM:    chat,
		Screen: screen.NewExecutor(d, s, p),
		Guard:  g,
		Now:    func() time.Time { return time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC) },
		Cfg: agent.Config{
			Actor:      llm.Role{Model: "test/actor", MaxTokens: 700},
			Extractor:  llm.Role{Model: "test/extractor", MaxTokens: 3000},
			MaxSteps:   20,
			Perception: p,
			Summary:    f,
			TodayFrom:  "app",
		},
	}
	return a, g
}

func gray() *fakes.Driver { return fakes.NewDriver(1280, 800, color.RGBA{220, 220, 220, 255}) }

// clicksAt cuenta las pulsaciones del boton izquierdo en (x,y).
func clicksAt(d *fakes.Driver, x, y int) int {
	n := 0
	for _, e := range d.Events() {
		if e.Pointer && e.Buttons&1 == 1 && e.X == x && e.Y == y {
			n++
		}
	}
	return n
}

func inputEvents(d *fakes.Driver) int {
	n := 0
	for _, e := range d.Events() {
		if (e.Pointer && e.Buttons != 0) || (!e.Pointer && e.Keysym != 0) {
			n++
		}
	}
	return n
}

const orient = `{"hoy":"Hoy: 14/10/2026","formato_fecha":"DD/MM/AAAA","titulo":"App","texto_visible":"App","menus":["Archivo"]}`

// st arma un estado de tarea para pruebas unitarias del guardrail.
func st(instr, intent, target string, values map[string]string) *agent.State {
	return &agent.State{Instruction: instr, Class: agent.Classification{Intent: intent, Target: target, Values: values,
		Question: intent == agent.IntentQuery}, Phase: agent.PhaseAct}
}

func page(cols []string, rows ...[]string) agent.Page {
	p := agent.Page{Columns: cols}
	for _, r := range rows {
		row := agent.Row{}
		for i, v := range r {
			row[cols[i]] = v
		}
		p.Rows = append(p.Rows, row)
	}
	return p
}

func commit(target, what string) agent.Action {
	return agent.Action{Kind: agent.KindCommit, Phase: agent.PhaseAct, X: 600, Y: 700, Target: target, What: what}
}

func extract(purpose, key string, filters map[string]string) agent.Action {
	return agent.Action{Kind: agent.KindExtract, Purpose: purpose, KeyCol: key, Filters: filters}
}
