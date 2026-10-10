//go:build desktop && live

package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/agent"
	"screenoperator/internal/config"
	"screenoperator/internal/harness"
	"screenoperator/internal/tasks"
)

// loadEnvFile carga SO_ENV_FILE (KEY=VALUE) sin pisar variables ni imprimir valores.
func loadEnvFile(t *testing.T) {
	t.Helper()
	p := os.Getenv("SO_ENV_FILE")
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("SO_ENV_FILE ilegible: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			t.Setenv(k, v)
		}
	}
}

type liveEnv struct {
	desk     *harness.Desktop
	ws       string
	password string
	mgr      *tasks.Manager
}

// setup arma el agente real en proceso (Task Manager + DriverDialer + Runner)
// contra el escritorio de .env y resetea el escritorio antes y despues.
func setup(t *testing.T) *liveEnv {
	t.Helper()
	loadEnvFile(t)
	raw, pw, tok := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD"), os.Getenv("SO_DESKTOP_TOKEN")
	if raw == "" || pw == "" || tok == "" || os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("ENVIRONMENTAL: faltan SO_DESKTOP_URL, SO_DESKTOP_PASSWORD, SO_DESKTOP_TOKEN u OPENROUTER_API_KEY (build tags desktop y live)")
	}
	base, ws, err := harness.DesktopBase(raw)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := config.NewLoader("../../config", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	traceDir := os.Getenv("SO_E6_TRACE_DIR")
	if traceDir == "" {
		traceDir = filepath.Join(os.TempDir(), "so-e6-traces")
	}
	red := tasks.NewRedactor("", pw, tok, os.Getenv("OPENROUTER_API_KEY"), os.Getenv("TEAM_TOKEN"))
	log := tasks.NewLogger(testWriter{t}, red)
	mgr := tasks.NewManager(tasks.Options{
		Runner:   &agent.Runner{Loader: loader, Getenv: os.Getenv},
		Dialer:   &agent.DriverDialer{Loader: loader},
		Budget:   tasks.Budget{Total: 300 * time.Second, Margin: 30 * time.Second},
		Logger:   log,
		Redactor: red,
		TraceDir: traceDir,
	})
	desk := &harness.Desktop{BaseURL: base, Token: tok}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := desk.Reset(ctx); err != nil {
		t.Fatalf("/__reset: %v", err)
	}
	time.Sleep(10 * time.Second) // el navegador del kiosko se reinicia
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
		if err := desk.Reset(ctx); err != nil {
			t.Errorf("/__reset final: %v", err)
		}
	})
	t.Logf("trazas en %s", traceDir)
	return &liveEnv{desk: desk, ws: ws, password: pw, mgr: mgr}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// run ejecuta una prueba del material y devuelve estado y resumen.
func (e *liveEnv) run(t *testing.T, caseFile string) (harness.Case, tasks.Snapshot, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../material/pruebas", caseFile))
	if err != nil {
		t.Fatal(err)
	}
	c, err := harness.ParseCase(caseFile, raw)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	id, err := e.mgr.Submit(tasks.Request{Instruction: c.Instruccion, DesktopURL: e.ws, DesktopSecrets: []tasks.Secret{tasks.Secret(e.password)}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		snap, _ := e.mgr.Get(id)
		if snap.Status != tasks.StatusRunning {
			u, _ := e.mgr.Metrics(id)
			t.Logf("%s: estado=%v en %v, pasos=%d tokens=%d/%d costo=%.4f US$\nresumen:\n%s", c.ID, snap.Status,
				time.Since(start).Round(time.Second), u.Steps, u.TokensIn, u.TokensOut, u.CostUSD, snap.Summary)
			return c, snap, raw
		}
		if time.Since(start) > 310*time.Second {
			t.Fatal("la tarea no termino en 310 s")
		}
		time.Sleep(2 * time.Second)
	}
}

func TestE6_3_S6_LiveE1CreatesClient(t *testing.T) {
	e := setup(t)
	_, snap, raw := e.run(t, "E1-crear-cliente.json")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := e.desk.Check(ctx, raw)
	if err != nil {
		t.Fatalf("/__check: %v", err)
	}
	t.Logf("/__check: pass=%v %+v", res.Pass, res.Checks)
	if snap.Status != tasks.StatusDone {
		t.Fatalf("la tarea no termino como terminada: %v", snap.Status)
	}
	if !res.Pass {
		t.Fatalf("/__check pass=false: %+v", res.Checks)
	}
}

func TestE6_5_S5_LiveE3CountsOverdueInvoices(t *testing.T) {
	e := setup(t)
	_, snap, _ := e.run(t, "E3-facturas-vencidas.json")
	if snap.Status != tasks.StatusDone {
		t.Fatalf("estado = %v", snap.Status)
	}
	found := false
	for _, l := range strings.Split(snap.Summary, "\n") {
		if strings.HasPrefix(l, "respuesta: 45") {
			found = true
		}
	}
	if !found {
		t.Fatalf("el resumen no contiene 'respuesta: 45':\n%s", snap.Summary)
	}
}
