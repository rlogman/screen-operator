package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/tasks"
)

// --- 3.5 Logs sin secretos ---

type wrapErr struct{ msg string }

func (e *wrapErr) Error() string { return e.msg }

func TestE3_5_S1_LogsNeverContainSecrets(t *testing.T) {
	const (
		password = "P4ss-xyz"
		team     = "team-tok-123"
		orKey    = "sk-or-v1-abcdef"
	)
	// Un agente falso descuidado: intenta registrar todo lo que tiene a mano.
	r := tasks.RunnerFunc(func(ctx context.Context, job *tasks.Job) tasks.Result {
		job.Log.Info("paso", tasks.LogKeyPhase, "agente", "paso", 1, "accion", "click", "duracion_ms", 120, "costo_usd", 0.001)
		job.Log.Info("request", tasks.LogKeyPhase, "agente", "req", job.Request, "pw", job.Request.Password())
		job.Log.Info("crudo "+job.Request.Password().Reveal(), tasks.LogKeyPhase, "agente",
			"pw_raw", job.Request.Password().Reveal(), "env", team+" "+orKey,
			"err", &wrapErr{"dial wss://u:" + password + "@h/websockify"},
			slog.Group("detalle", "pw", job.Request.Password().Reveal()))
		job.Log.Info("sin fase explicita " + orKey)
		return tasks.Result{Status: tasks.StatusDone, Summary: "hecho con " + password + " y " + team}
	})
	buf := &syncBuffer{}
	red := tasks.NewRedactor("", team, orKey)
	log := tasks.NewLogger(buf, red)
	mgr := tasks.NewManager(tasks.Options{Runner: r, Budget: tasks.Budget{Total: 10 * time.Second}, Logger: log, Redactor: red})
	defer mgr.Shutdown(context.Background())
	srv, err := NewServer(repoContract(t), team, mgr, log)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, srv: srv, mgr: mgr, log: buf}
	body := `{"tarea":"Registra al cliente","escritorio":{"url":"wss://user:` + password + `@desk.example/websockify?token=` + team + `","password":"` + password + `"}}`
	res, b := h.do(http.MethodPost, "/tasks", "Bearer "+team, body)
	if res.StatusCode != 200 {
		t.Fatalf("POST = %d %s", res.StatusCode, b)
	}
	var created map[string]string
	_ = json.Unmarshal(b, &created)
	deadline := time.Now().Add(5 * time.Second)
	var st map[string]any
	for {
		r2, b2 := h.do(http.MethodGet, "/tasks/"+created["task_id"], "Bearer "+team, "")
		_ = json.Unmarshal(b2, &st)
		if r2.StatusCode == 200 && st["estado"] != "en_curso" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("la tarea no termino")
		}
		time.Sleep(10 * time.Millisecond)
	}
	out := buf.String()
	// Los intentos de registrar secretos quedaron, pero redactados.
	if !strings.Contains(out, `"pw_raw":"[redactado]"`) || !strings.Contains(out, `crudo [redactado]`) {
		t.Fatalf("los intentos de log no aparecen redactados:\n%s", out)
	}
	if !strings.Contains(out, `"accion":"click"`) {
		t.Fatalf("faltan los pasos del agente en el log:\n%s", out)
	}
	for _, s := range []string{password, team, orKey} {
		if strings.Contains(out, s) {
			t.Fatalf("el log contiene el secreto %q:\n%s", s, out)
		}
	}
	// El resumen tampoco los repite.
	if s, _ := st["resumen"].(string); strings.Contains(s, password) || strings.Contains(s, team) {
		t.Fatalf("el resumen contiene un secreto: %q", s)
	}
}

func TestE3_5_S2_EveryTaskLogLineIsJSONWithFields(t *testing.T) {
	r := tasks.RunnerFunc(func(ctx context.Context, job *tasks.Job) tasks.Result {
		for i := 1; i <= 3; i++ {
			job.Log.Info("paso", tasks.LogKeyPhase, "agente", "paso", i, "accion", "tecla", "duracion_ms", 5, "costo_usd", 0.0002)
		}
		job.Log.Warn("linea sin fase") // el handler agrega fase por defecto
		return tasks.Result{Status: tasks.StatusFailed, Summary: "no se encontro el cliente"}
	})
	h := newHarness(t, r, tasks.Budget{})
	id := h.create(validBody)
	h.waitFinal(id, 5*time.Second)
	lines := strings.Split(strings.TrimSpace(h.log.String()), "\n")
	taskLines := 0
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("linea de log no es JSON valido: %q (%v)", ln, err)
		}
		if _, ok := m["fase"].(string); !ok {
			t.Fatalf("linea sin fase: %s", ln)
		}
		ts, ok := m["timestamp"].(string)
		if !ok {
			t.Fatalf("linea sin timestamp: %s", ln)
		}
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Fatalf("timestamp %q no es RFC 3339: %v", ts, err)
		}
		if m["task_id"] == id {
			taskLines++
		}
		// Toda linea de la tarea (todo salvo arranque) lleva task_id.
		if m["fase"] != "http" || strings.HasPrefix(m["ruta"].(string), "/tasks") {
			if m["task_id"] != id {
				t.Fatalf("linea de la tarea sin task_id: %s", ln)
			}
		}
	}
	if taskLines < 6 {
		t.Fatalf("se esperaban al menos 6 lineas con task_id, hubo %d:\n%s", taskLines, h.log.String())
	}
}
