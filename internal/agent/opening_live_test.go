//go:build desktop && live

package agent

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/llm"
	"screenoperator/internal/rfb"
	"screenoperator/internal/screen"
)

// E10.1-S3 contra el escritorio real recien reseteado y el modelo real.
// Correr siempre envuelto en scripts/with_desktop.sh (acceso exclusivo) y con
// el entorno de .env cargado. Sin SO_DESKTOP_URL, SO_DESKTOP_PASSWORD,
// SO_DESKTOP_TOKEN u OPENROUTER_API_KEY se salta (ENVIRONMENTAL).
func TestE10_1_S3_LiveOpeningClosesNewModal(t *testing.T) {
	u, pw, tok := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD"), os.Getenv("SO_DESKTOP_TOKEN")
	if u == "" || pw == "" || tok == "" || os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("ENVIRONMENTAL: faltan SO_DESKTOP_URL, SO_DESKTOP_PASSWORD, SO_DESKTOP_TOKEN u OPENROUTER_API_KEY (build tags desktop y live)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	reset := func() {
		if code, err := rfb.Reset(context.Background(), nil, u, tok); err != nil || code/100 != 2 {
			t.Logf("reset del escritorio: codigo %d, %v", code, err)
		}
	}
	reset()
	t.Cleanup(reset)

	l, err := config.NewLoader("../../config", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := l.ForTask(u, "")
	if err != nil {
		t.Fatal(err)
	}
	surf, perc, err := screen.FromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	models, err := llm.ModelsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := models.ClientOptions(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	dialogs, err := DialogPolicyFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Tras /__reset el kiosko tarda en aceptar VNC: hasta 3 intentos.
	var d screen.Driver
	for i := 0; i < 3; i++ {
		time.Sleep(5 * time.Second)
		if d, err = screen.Open(ctx, u, pw, surf, 15*time.Second); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("no se pudo abrir el escritorio: %v", err)
	}
	defer d.Close()

	tr := &recTracer{}
	a := &Agent{
		LLM:    llm.NewMeter(llm.NewClient(opts), 0.05),
		Screen: screen.NewExecutor(d, surf, perc),
		Trace:  tr,
		Log:    slog.New(slog.NewTextHandler(testLogWriter{t}, nil)),
		Cfg:    Config{Actor: models.Role("actor"), Perception: perc, Dialogs: dialogs, MaxSteps: 5},
	}
	a.init()
	a.st = &State{Instruction: "Abre la aplicacion"}
	a.st.enter(PhaseOrient)
	if _, err := a.stableCapture(ctx); err != nil {
		t.Fatal(err)
	}
	fp, failure, err := a.openingCheck(ctx)
	if err != nil || failure != nil {
		t.Fatalf("openingCheck: err=%v falla=%+v", err, failure)
	}
	var closed, free bool
	for _, s := range tr.steps {
		t.Logf("traza: %s | %s", s.Action, s.Reason)
		if strings.Contains(s.Reason, "diálogo informativo cerrado") {
			closed = true
		}
		if strings.Contains(s.Reason, "libre de dialogos") {
			free = true
		}
	}
	if !closed {
		t.Fatalf("el modal nuevo no se cerro (traza sin 'diálogo informativo cerrado'); huella %q", fp)
	}
	if !free {
		t.Fatal("la revision siguiente no confirmo la pantalla principal libre de dialogos")
	}
	// La captura siguiente muestra la pantalla principal: sin el velo oscuro
	// del modal (el fondo de la app es claro; con el modal queda gris medio).
	out, err := a.Screen.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := out.Screen.RGBAAt(640, 650)
	if luma := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B); luma < 180 {
		t.Fatalf("la captura siguiente sigue velada (luma %.0f en (640,650)): el modal no se cerro", luma)
	}
	if !strings.Contains(strings.ToLower(fp), "v4.2") {
		t.Logf("aviso: la huella %q no trae la version v4.2", fp)
	}
	in, outTok, cost, calls := a.LLM.(*llm.Meter).Totals()
	t.Logf("huella=%q llamadas=%d tokens=%d/%d costo=US$%.4f", fp, calls, in, outTok, cost)
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
