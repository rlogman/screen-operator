package agent

import (
	"image"
	"image/color"
	_ "image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/fakes"
	"screenoperator/internal/llm"
	"screenoperator/internal/screen"
	"screenoperator/internal/summary"
	"screenoperator/internal/trace"
)

// recTracer guarda los pasos de la traza en memoria.
type recTracer struct {
	mu    sync.Mutex
	steps []trace.Step
	shots int
}

func (r *recTracer) Screenshot([]byte) error {
	r.mu.Lock()
	r.shots++
	r.mu.Unlock()
	return nil
}

func (r *recTracer) Step(s trace.Step) error {
	r.mu.Lock()
	r.steps = append(r.steps, s)
	r.mu.Unlock()
	return nil
}

func (r *recTracer) phases() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.steps {
		if len(out) == 0 || out[len(out)-1] != s.Phase {
			out = append(out, s.Phase)
		}
	}
	return out
}

// fastSurface es vnc-kiosk con tiempos de milisegundos para tests en memoria.
func fastSurface() (screen.Surface, screen.Perception) {
	var s screen.Surface
	s.ID, s.Transport = "test", "rfb"
	s.Screen.Width, s.Screen.Height = 1280, 800
	s.Input.ClickHoldMS = 1
	s.Input.KeyHoldMS = 0
	s.Input.ReplaceText.Strategy = "select_all_then_type"
	s.Input.ReplaceText.Chord = []string{"ctrl", "a"}
	s.Input.Scroll.WheelClicksPerStep = 1
	s.Settle = screen.SettleConfig{MinWaitMS: 0, MaxWaitMS: 60, PollMS: 10, StableFrames: 1, DiffThreshold: 0.002}
	var p screen.Perception
	p.Screenshot.Format, p.Screenshot.JPEGQuality, p.Screenshot.MaxWidth = "jpeg", 60, 1280
	p.Zoom.Enabled, p.Zoom.Factor, p.Zoom.MaxPerStep = true, 2, 1
	return s, p
}

func testSummary(t *testing.T) summary.Format {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.ForTask("wss://x/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := summary.FromConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// newTestAgent arma un Agent con un LLM guionado y un driver en memoria.
func newTestAgent(t *testing.T, chat llm.Chatter, d *fakes.Driver, maxSteps int) (*Agent, *recTracer) {
	t.Helper()
	s, p := fastSurface()
	tr := &recTracer{}
	a := &Agent{
		LLM:    chat,
		Screen: screen.NewExecutor(d, s, p),
		Trace:  tr,
		Now:    func() time.Time { return time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC) },
		Cfg: Config{
			Actor:      llm.Role{Model: "test/actor", MaxTokens: 700},
			Extractor:  llm.Role{Model: "test/extractor", MaxTokens: 3000},
			MaxSteps:   maxSteps,
			Perception: p,
			Summary:    testSummary(t),
			TodayFrom:  "app",
		},
	}
	return a, tr
}

func gray() *fakes.Driver { return fakes.NewDriver(1280, 800, color.RGBA{220, 220, 220, 255}) }

func loadFixture(t *testing.T, name string) image.Image {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// Respuestas guionadas frecuentes.
const (
	jsonClassifyEdit  = `{"intencion":"editar","entidad":"cliente","objetivo":"Distribuciones El Faro","valores":{"direccion":"Calle 80 # 24-15"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"buscar el cliente, editar la direccion, guardar y releer"}`
	jsonClassifyQuery = `{"intencion":"consultar","entidad":"factura","objetivo":"Ferretería Ruiz","valores":{},"pregunta":true,"datos_pedidos":["cantidad de facturas vencidas"],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"filtrar facturas y contar"}`
	jsonOrient        = `{"hoy":"Hoy: 14/10/2026","formato_fecha":"DD/MM/AAAA","titulo":"App de prueba","texto_visible":"App de prueba","menus":["Archivo"]}`
)

func requestText(r llm.Request) string {
	var b strings.Builder
	for _, m := range r.Messages {
		b.WriteString(m.Text())
		b.WriteString("\n")
	}
	return b.String()
}

func requestImages(r llm.Request) int {
	n := 0
	for _, m := range r.Messages {
		n += m.Images()
	}
	return n
}
