package agent

// Tests del grounder opcional (historia 8.1) con LLM y grounder falsos.

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"screenoperator/internal/config"
	"screenoperator/internal/fakes"
	"screenoperator/internal/llm"
)

const (
	grounderModel     = "test/grounder"
	jsonClickGuardar  = `{"fase":"actuar","action":"click","x":100,"y":40,"elemento":"botón Guardar","reason":"guardar el cambio"}`
	jsonClickNoGround = `{"fase":"actuar","action":"click","x":100,"y":40,"reason":"guardar el cambio"}`
)

// fakeGrounder devuelve siempre el mismo punto y registra las descripciones.
type fakeGrounder struct {
	x, y  int
	descs []string
}

func (f *fakeGrounder) Ground(_ context.Context, _ *image.RGBA, desc string) (int, int, llm.Usage, error) {
	f.descs = append(f.descs, desc)
	return f.x, f.y, llm.Usage{}, nil
}

// clickPoints devuelve los puntos donde se presiono el boton izquierdo.
func clickPoints(d *fakes.Driver) []image.Point {
	var out []image.Point
	var prev uint8
	for _, e := range d.Events() {
		if !e.Pointer {
			continue
		}
		if e.Buttons&1 != 0 && prev&1 == 0 {
			out = append(out, image.Pt(e.X, e.Y))
		}
		prev = e.Buttons
	}
	return out
}

func runGuardar(t *testing.T, chat *fakes.ScriptedLLM, setup func(a *Agent)) (*fakes.Driver, Outcome) {
	t.Helper()
	d := gray()
	a, _ := newTestAgent(t, chat, d, 4)
	setup(a)
	out := a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	return d, out
}

func TestE8_1_S1_GrounderPointIsClicked(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient, jsonClickGuardar, jsonGiveUp)
	g := &fakeGrounder{x: 300, y: 200}
	d, out := runGuardar(t, chat, func(a *Agent) {
		a.Cfg.Grounder = llm.Role{Model: grounderModel}
		a.Locator = g
	})
	pts := clickPoints(d)
	if len(pts) != 1 || pts[0] != image.Pt(300, 200) {
		t.Fatalf("clics = %v; se esperaba uno en (300,200). Historial %v", pts, out.State.History)
	}
	if len(g.descs) != 1 || g.descs[0] != "botón Guardar" {
		t.Fatalf("descripciones al grounder = %v", g.descs)
	}
	// El actor recibe la instruccion de describir el elemento.
	for _, r := range chat.Calls() {
		if r.Purpose == "actuar" && !strings.Contains(requestText(r), `"elemento"`) {
			t.Fatalf("el prompt de actuar no pide \"elemento\" con el grounder activo")
		}
	}
}

// Con el UITARS real sobre un LLM falso: la vista es 1280x800, smart_resize la
// lleva a 1288x812 y el punto (302,203) de esa imagen vuelve a (300,200).
func TestE8_1_S1_UITARSOverLLMMapsSmartResize(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient, jsonClickGuardar,
		"Action: click(point='<point>302 203</point>')", jsonGiveUp)
	d, out := runGuardar(t, chat, func(a *Agent) {
		a.Cfg.Grounder = llm.Role{Model: grounderModel, MaxTokens: 64}
	})
	pts := clickPoints(d)
	if len(pts) != 1 || pts[0] != image.Pt(300, 200) {
		t.Fatalf("clics = %v; se esperaba uno en (300,200). Historial %v", pts, out.State.History)
	}
	var g *llm.Request
	for _, r := range chat.Calls() {
		if r.Model == grounderModel {
			r := r
			g = &r
		}
	}
	if g == nil || g.Purpose != "ubicar" || requestImages(*g) != 1 || !strings.Contains(requestText(*g), "botón Guardar") {
		t.Fatalf("llamada al grounder = %+v", g)
	}
	if g.JSON {
		t.Fatal("UI-TARS no responde JSON: el request no debe pedir json_object")
	}
}

func TestE8_1_S2_WithoutGrounderActorCoordinatesAreUsed(t *testing.T) {
	for _, tc := range []struct{ name, click string }{{"con elemento", jsonClickGuardar}, {"sin elemento", jsonClickNoGround}} {
		t.Run(tc.name, func(t *testing.T) {
			chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient, tc.click, jsonGiveUp)
			g := &fakeGrounder{x: 300, y: 200}
			d, out := runGuardar(t, chat, func(a *Agent) { a.Locator = g }) // sin models.roles.grounder
			pts := clickPoints(d)
			if len(pts) != 1 || pts[0] != image.Pt(100, 40) {
				t.Fatalf("clics = %v; se esperaba uno en (100,40). Historial %v", pts, out.State.History)
			}
			if len(g.descs) != 0 {
				t.Fatalf("el grounder se consulto sin el rol configurado: %v", g.descs)
			}
			for _, r := range chat.Calls() {
				if strings.Contains(requestText(r), "UBICADOR DE ELEMENTOS") {
					t.Fatal("sin grounder el prompt no debe cambiar")
				}
			}
		})
	}
}

func TestE8_1_S2_GrounderFailureKeepsActorPoint(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient, jsonClickGuardar, "no se", jsonGiveUp)
	d, out := runGuardar(t, chat, func(a *Agent) { a.Cfg.Grounder = llm.Role{Model: grounderModel} })
	if pts := clickPoints(d); len(pts) != 1 || pts[0] != image.Pt(100, 40) {
		t.Fatalf("clics = %v; con el grounder sin punto se esperaba (100,40). Historial %v", pts, out.State.History)
	}
}

func TestE8_1_SmartResizeAndParse(t *testing.T) {
	for _, c := range []struct{ h, w, wh, ww int }{{800, 1280, 812, 1288}, {1080, 1920, 1092, 1932}, {28, 28, 280, 280}} {
		h, w, err := SmartResize(c.h, c.w)
		if err != nil || h != c.wh || w != c.ww {
			t.Fatalf("SmartResize(%d,%d) = %d,%d,%v; se esperaba %d,%d", c.h, c.w, h, w, err, c.wh, c.ww)
		}
	}
	for in, want := range map[string]image.Point{
		"Action: click(point='<point>197 525</point>')":        image.Pt(197, 525),
		"Thought: ok\nAction: click(start_box='(197,525)')":    image.Pt(197, 525),
		"click(start_box='<|box_start|>(197,525)<|box_end|>')": image.Pt(197, 525),
		"click(start_box='(190,520,204,530)')":                 image.Pt(197, 525),
		"Action: click(point='<point>197.4 524.6</point>')":    image.Pt(197, 525),
	} {
		x, y, err := ParseUITARSPoint(in)
		if err != nil || image.Pt(x, y) != want {
			t.Fatalf("ParseUITARSPoint(%q) = %d,%d,%v; se esperaba %v", in, x, y, err, want)
		}
	}
	if _, _, err := ParseUITARSPoint("no veo el elemento"); err == nil {
		t.Fatal("sin punto debia ser error")
	}
}

// Apagado por defecto: base.yaml no configura el grounder; un overlay que lo
// activa pasa la validacion estricta de configuracion.
func TestE8_1_S2_GrounderOffByDefaultAndOverlayValidates(t *testing.T) {
	load := func(getenv func(string) string) llm.Models {
		t.Helper()
		l, err := config.NewLoader("../../config", getenv)
		if err != nil {
			t.Fatal(err)
		}
		c, err := l.ForTask("wss://x/websockify", "")
		if err != nil {
			t.Fatal(err)
		}
		m, err := llm.ModelsFromConfig(c)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if r, ok := load(func(string) string { return "" }).GrounderRole(); ok {
		t.Fatalf("base.yaml configura el grounder (%s); debe venir apagado", r.Model)
	}
	ov := filepath.Join(t.TempDir(), "grounder.yaml")
	body := "models:\n  roles:\n    grounder: { model: bytedance/ui-tars-1.5-7b, temperature: 0, max_tokens: 64, price_in_usd_per_mtok: 0.10, price_out_usd_per_mtok: 0.20 }\n"
	if err := os.WriteFile(ov, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := load(func(k string) string {
		if k == config.EnvOverlay {
			return ov
		}
		return ""
	})
	if r, ok := m.GrounderRole(); !ok || r.Model != "bytedance/ui-tars-1.5-7b" {
		t.Fatalf("con el overlay el grounder = %+v, %v", r, ok)
	}
}
