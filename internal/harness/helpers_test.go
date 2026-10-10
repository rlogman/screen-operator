package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// memTransport atiende peticiones con handlers en memoria, por host: el sandbox
// de los tests no deja bindear puertos (httptest.NewServer da EPERM).
type memTransport map[string]http.Handler

func (m memTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h, ok := m[req.URL.Host]
	if !ok {
		return nil, &os.PathError{Op: "dial", Path: req.URL.Host, Err: os.ErrNotExist}
	}
	if req.Body == nil {
		req.Body = http.NoBody
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// fakeClock: Sleep avanza el reloj al instante.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

// event es una llamada vista por los servidores falsos.
type event struct {
	At   time.Duration // desde el primer evento
	What string
	Body string
}

// world junta escritorio y agente falsos con un registro comun de llamadas.
type world struct {
	t     *testing.T
	clock *fakeClock
	mu    sync.Mutex
	t0    time.Time
	calls []event

	// agente
	tasks     map[string]time.Time // id -> instante del POST
	finishAt  time.Duration        // < 0: nunca termina
	estado    string               // estado final
	resumen   string
	lastPass  string
	nextID    int
	metrics   Metrics
	noMetrics bool

	// escritorio
	checkPass   bool
	checkStatus int // 0 = 200
	checkBody   string
}

func newWorld(t *testing.T) *world {
	w := &world{t: t, clock: newFakeClock(), tasks: map[string]time.Time{},
		finishAt: 6 * time.Second, estado: "terminada", resumen: "hecho", checkPass: true}
	w.t0 = w.clock.Now()
	return w
}

func (w *world) log(what, body string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, event{At: w.clock.Now().Sub(w.t0), What: what, Body: body})
}

func (w *world) events() []event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]event(nil), w.calls...)
}

func (w *world) desktopHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /__reset", func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer desk-tok" {
			rw.WriteHeader(401)
			return
		}
		w.log("reset", "")
		rw.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /__check", func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer desk-tok" {
			rw.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.log("check", string(b))
		if w.checkStatus != 0 {
			rw.WriteHeader(w.checkStatus)
			rw.Write([]byte(w.checkBody))
			return
		}
		json.NewEncoder(rw).Encode(map[string]any{"pass": w.checkPass, "checks": []map[string]any{
			{"nombre": "cambios esperados", "ok": w.checkPass},
			{"nombre": "sin acciones fuera de la tarea", "ok": true},
			{"nombre": "sin registros duplicados", "ok": true},
		}})
	})
	return mux
}

func (w *world) agentHandler() http.Handler {
	mux := http.NewServeMux()
	auth := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer team-tok" }
	mux.HandleFunc("POST /tasks", func(rw http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			rw.WriteHeader(401)
			return
		}
		var body struct {
			Tarea      string `json:"tarea"`
			Escritorio struct {
				URL      string `json:"url"`
				Password string `json:"password"`
			} `json:"escritorio"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.mu.Lock()
		w.nextID++
		id := fmt.Sprintf("t%d", w.nextID)
		w.tasks[id] = w.clock.Now()
		w.lastPass = body.Escritorio.Password
		w.mu.Unlock()
		w.log("post", body.Tarea+"|"+body.Escritorio.URL)
		json.NewEncoder(rw).Encode(map[string]string{"task_id": id})
	})
	mux.HandleFunc("GET /tasks/{id}", func(rw http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			rw.WriteHeader(401)
			return
		}
		id := r.PathValue("id")
		w.log("get", id)
		w.mu.Lock()
		started, ok := w.tasks[id]
		w.mu.Unlock()
		if !ok {
			rw.WriteHeader(404)
			json.NewEncoder(rw).Encode(map[string]string{"estado": "fallida", "resumen": "desconocida"})
			return
		}
		st := map[string]string{"estado": "en_curso", "resumen": ""}
		if w.finishAt >= 0 && w.clock.Now().Sub(started) >= w.finishAt {
			st = map[string]string{"estado": w.estado, "resumen": w.resumen}
		}
		json.NewEncoder(rw).Encode(st)
	})
	mux.HandleFunc("GET /tasks/{id}/metrics", func(rw http.ResponseWriter, r *http.Request) {
		if !auth(r) || w.noMetrics {
			rw.WriteHeader(404)
			return
		}
		json.NewEncoder(rw).Encode(w.metrics)
	})
	return mux
}

func (w *world) runner(out io.Writer) *Runner {
	client := &http.Client{Transport: memTransport{"desk.test": w.desktopHandler(), "agent.test": w.agentHandler()}}
	return &Runner{
		Desktop:   &Desktop{BaseURL: "https://desk.test", Token: "desk-tok", Client: client},
		Agent:     &Agent{BaseURL: "http://agent.test", Token: "team-tok", Client: client},
		DesktopWS: "wss://desk.test/websockify",
		Password:  "vnc-pass-123",
		Secrets:   []string{"team-tok", "desk-tok"},
		Out:       out,
		Now:       w.clock.Now,
		Sleep:     w.clock.Sleep,
	}
}

// writeCase escribe un caso en dir y devuelve la ruta.
func writeCase(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func decodeJSONL(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("linea JSONL invalida %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func mustCase(t *testing.T, body string) Case {
	t.Helper()
	c, err := ParseCase("caso.json", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
