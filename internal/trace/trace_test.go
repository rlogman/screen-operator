package trace_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/tasks"
	"screenoperator/internal/trace"
)

const (
	password = "Vnc-S3cret-77"
	teamTok  = "team-token-abc123"
	orKey    = "sk-or-v1-deadbeef"
)

// fakeJPEG es un JPEG minimo (SOI ... EOI) distinto por paso.
func fakeJPEG(n int) []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, byte(n), 0xFF, 0xD9}
}

// fakeAgent da tres pasos con una captura cada uno; en el segundo el modelo
// "repite" la contraseña y los tokens en su razon.
func fakeAgent() tasks.Runner {
	return tasks.RunnerFunc(func(_ context.Context, job *tasks.Job) tasks.Result {
		steps := []trace.Step{
			{Phase: "percepcion", Action: "captura inicial", Reason: "ver la pantalla", TokensIn: 1200, TokensOut: 80, CostUSD: 0.0003, Duration: 900 * time.Millisecond},
			{Phase: "accion", Action: "click(640,400)", Reason: "la clave es " + password + " y el token " + teamTok + " / " + orKey, TokensIn: 1300, TokensOut: 60, CostUSD: 0.0004, Duration: 1200 * time.Millisecond},
			{Phase: "verificacion", Action: "leer total", Reason: "el total es 45", TokensIn: 1100, TokensOut: 40, CostUSD: 0.0002, Duration: 700 * time.Millisecond},
		}
		for i, s := range steps {
			if err := job.Trace.Screenshot(fakeJPEG(i + 1)); err != nil {
				job.Log.Error("captura", "error", err)
			}
			if err := job.Trace.Step(s); err != nil {
				job.Log.Error("paso", "error", err)
			}
		}
		return tasks.Result{Status: tasks.StatusDone, Summary: "respuesta: 45"}
	})
}

// runTask corre una tarea con el Task Manager real y espera su fin.
func runTask(t *testing.T, traceDir string, r tasks.Runner) (string, *tasks.Manager) {
	t.Helper()
	red := tasks.NewRedactor("", teamTok, orKey)
	m := tasks.NewManager(tasks.Options{
		Runner: r, Budget: tasks.Budget{Total: 30 * time.Second, Margin: time.Second},
		Logger: tasks.NewLogger(&bytes.Buffer{}, red), Redactor: red, TraceDir: traceDir,
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	id, err := m.Submit(tasks.Request{Instruction: "¿Cuántas facturas vencidas tiene Ferretería Ruiz?",
		DesktopURL: "wss://desk.example/websockify", DesktopSecrets: []tasks.Secret{password}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, _ := m.Get(id)
		if s.Status != tasks.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("la tarea no termino")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// La traza se cierra antes de publicar el estado final.
	return id, m
}

func TestE5_6_S1_StepsJSONLAndScreenshots(t *testing.T) {
	root := filepath.Join(t.TempDir(), "traces")
	id, m := runTask(t, root, fakeAgent())
	dir := filepath.Join(root, id)

	f, err := os.Open(filepath.Join(dir, "steps.jsonl"))
	if err != nil {
		t.Fatalf("falta traces/<task_id>/steps.jsonl: %v", err)
	}
	defer f.Close()
	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l map[string]any
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("linea invalida %q: %v", sc.Text(), err)
		}
		lines = append(lines, l)
	}
	if len(lines) != 3 {
		t.Fatalf("lineas = %d, se esperaba una por paso (3)", len(lines))
	}
	for i, l := range lines {
		n := float64(i + 1)
		if l["paso"] != n {
			t.Fatalf("paso %d = %v", i, l["paso"])
		}
		for _, k := range []string{"fase", "accion", "razon", "tokens_entrada", "tokens_salida", "costo_usd", "duracion_ms", "timestamp"} {
			if _, ok := l[k]; !ok {
				t.Fatalf("la linea %d no tiene %q: %v", i+1, k, l)
			}
		}
		caps, _ := l["capturas"].([]any)
		want := fmt.Sprintf("step-%d.jpg", i+1)
		if len(caps) != 1 || caps[0] != want {
			t.Fatalf("capturas del paso %d = %v, se esperaba %s", i+1, caps, want)
		}
		img, err := os.ReadFile(filepath.Join(dir, want))
		if err != nil || !bytes.Equal(img, fakeJPEG(i+1)) {
			t.Fatalf("%s: %v", want, err)
		}
	}
	if lines[1]["duracion_ms"].(float64) != 1200 || lines[0]["tokens_entrada"].(float64) != 1200 {
		t.Fatalf("valores: %v", lines)
	}

	// Los pasos alimentan las metricas de la tarea.
	u, ok := m.Metrics(id)
	if !ok || u.Steps != 3 || u.TokensIn != 3600 || u.TokensOut != 180 || u.CostUSD < 0.00089 || u.CostUSD > 0.00091 {
		t.Fatalf("metricas = %+v", u)
	}
}

func TestE5_6_S2_TraceHasNoSecrets(t *testing.T) {
	root := t.TempDir()
	runTask(t, root, fakeAgent())
	files := 0
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		files++
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, s := range []string{password, teamTok, orKey} {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s contiene un secreto", p)
			}
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s es legible por otros: %v", p, fi.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files != 4 {
		t.Fatalf("archivos = %d (steps.jsonl + 3 capturas)", files)
	}
	// La razon quedo redactada, no borrada.
	b, _ := os.ReadFile(filepath.Join(root, mustOnlyDir(t, root), "steps.jsonl"))
	if !strings.Contains(string(b), "[redactado]") {
		t.Fatalf("la razon no fue redactada: %s", b)
	}
	// Un task_id con separadores de ruta no escapa del directorio.
	if _, err := trace.New(root, "../fuera", nil, nil); err == nil {
		t.Fatal("task_id con ../ deberia rechazarse")
	}
}

func mustOnlyDir(t *testing.T, root string) string {
	t.Helper()
	es, err := os.ReadDir(root)
	if err != nil || len(es) != 1 {
		t.Fatalf("directorios en %s: %v %v", root, es, err)
	}
	return es[0].Name()
}

func TestE5_6_S3_NoTraceDirWritesNothing(t *testing.T) {
	cwd := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	t.Setenv(trace.EnvDir, "")

	id, m := runTask(t, "", fakeAgent())
	es, _ := os.ReadDir(cwd)
	if len(es) != 0 {
		t.Fatalf("sin SO_TRACE_DIR se escribio en disco: %v", es)
	}
	// Sin disco, los pasos igual cuentan para las metricas.
	if u, _ := m.Metrics(id); u.Steps != 3 {
		t.Fatalf("metricas sin traza = %+v", u)
	}
	// Recorder sin directorio y recorder nil: no escriben ni fallan.
	r, err := trace.New("", "x", nil, nil)
	if err != nil || r.Dir() != "" || r.Screenshot(fakeJPEG(1)) != nil || r.Step(trace.Step{}) != nil || r.Close() != nil {
		t.Fatalf("recorder sin directorio: %v", err)
	}
	var nilRec *trace.Recorder
	if nilRec.Step(trace.Step{}) != nil || nilRec.Screenshot(nil) != nil || nilRec.Close() != nil {
		t.Fatal("recorder nil deberia ignorar todo")
	}
	if es, _ := os.ReadDir(cwd); len(es) != 0 {
		t.Fatalf("se escribio en disco: %v", es)
	}
}
