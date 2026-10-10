package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/tasks"
)

const testToken = "t0k"

// repoContract lee contract.* de la configuracion real del repositorio.
func repoContract(t *testing.T) Contract {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ss, err := l.Surfaces()
	if err != nil || len(ss) == 0 {
		t.Fatalf("superficies: %v", err)
	}
	cfg, err := l.Load(ss[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ContractFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	t   *testing.T
	srv *Server
	mgr *tasks.Manager
	log *syncBuffer
}

// newHarness arma API + Task Manager con el runner dado y logs a un buffer.
func newHarness(t *testing.T, r tasks.Runner, budget tasks.Budget, secrets ...string) *harness {
	t.Helper()
	buf := &syncBuffer{}
	red := tasks.NewRedactor("", secrets...)
	log := tasks.NewLogger(buf, red)
	if budget.Total == 0 {
		budget = tasks.Budget{Total: 300 * time.Second, Margin: 30 * time.Second}
	}
	mgr := tasks.NewManager(tasks.Options{Runner: r, Budget: budget, Logger: log, Redactor: red})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	srv, err := NewServer(repoContract(t), testToken, mgr, log)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, srv: srv, mgr: mgr, log: buf}
}

func (h *harness) do(method, path, auth, body string) (*http.Response, []byte) {
	h.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func (h *harness) create(body string) string {
	h.t.Helper()
	res, b := h.do(http.MethodPost, "/tasks", "Bearer "+testToken, body)
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("POST /tasks = %d %s", res.StatusCode, b)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil || m["task_id"] == "" {
		h.t.Fatalf("POST /tasks cuerpo %s: %v", b, err)
	}
	return m["task_id"]
}

func (h *harness) status(id string) (int, map[string]any) {
	h.t.Helper()
	res, b := h.do(http.MethodGet, "/tasks/"+id, "Bearer "+testToken, "")
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		h.t.Fatalf("GET /tasks/%s cuerpo no JSON %q: %v", id, b, err)
	}
	return res.StatusCode, m
}

// waitFinal espera a que la tarea deje de estar en_curso.
func (h *harness) waitFinal(id string, within time.Duration) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(within)
	for {
		_, m := h.status(id)
		if m["estado"] != "en_curso" {
			return m
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("la tarea %s sigue en curso tras %v", id, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const validBody = `{"tarea":"Registra al cliente Papeleria Los Andes","escritorio":{"url":"wss://desk.example/websockify","password":"P4ss-xyz"}}`

// blockRunner bloquea hasta que se cancela el contexto o se libera release.
type blockRunner struct {
	release chan struct{}
	started chan string
}

func newBlockRunner() *blockRunner {
	return &blockRunner{release: make(chan struct{}), started: make(chan string, 16)}
}

func (b *blockRunner) Run(ctx context.Context, job *tasks.Job) tasks.Result {
	b.started <- job.ID
	select {
	case <-ctx.Done():
	case <-b.release:
	}
	return tasks.Result{Status: tasks.StatusDone, Summary: "respuesta: 45"}
}
