package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type memTransport map[string]http.Handler

func (m memTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	h, ok := m[req.URL.Host]
	if !ok {
		rec.WriteHeader(502)
		return rec.Result(), nil
	}
	h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// El binario: --runs, --cases y la salida runs/<fecha-hora>/{results.jsonl,report.*}.
func TestE5_1_S3_CLIRunWritesJSONLAndReport(t *testing.T) {
	var resets, posts atomic.Int32
	desk := http.NewServeMux()
	desk.HandleFunc("POST /__reset", func(w http.ResponseWriter, r *http.Request) { resets.Add(1) })
	desk.HandleFunc("POST /__check", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pass":true,"checks":[{"nombre":"cambios esperados","ok":true}]}`))
	})
	agent := http.NewServeMux()
	agent.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Write([]byte(`{"task_id":"abc"}`))
	})
	agent.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"estado": "terminada", "resumen": "respuesta: 45"})
	})
	agent.HandleFunc("GET /tasks/{id}/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pasos":4,"costo_usd":0.01}`))
	})
	httpClient = &http.Client{Transport: memTransport{"desk.test": desk, "agent.test": agent}}
	defer func() { httpClient = nil }()

	env := map[string]string{"SO_DESKTOP_URL": "wss://desk.test/websockify", "SO_DESKTOP_PASSWORD": "pw-123",
		"SO_DESKTOP_TOKEN": "dt", "TEAM_TOKEN": "tt"}
	out := filepath.Join(t.TempDir(), "runs")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"run", "--cases", "../../material/pruebas/E3*.json", "--runs", "3",
		"--agent", "http://agent.test", "--out", out, "--poll", "1ms"}, func(k string) string { return env[k] }, fixedNow, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("codigo %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if resets.Load() != 3 || posts.Load() != 3 {
		t.Fatalf("resets=%d posts=%d", resets.Load(), posts.Load())
	}
	dir := filepath.Join(out, "2026-10-10_150405")
	b, err := os.ReadFile(filepath.Join(dir, "results.jsonl"))
	if err != nil || strings.Count(string(b), "\n") != 3 {
		t.Fatalf("results.jsonl: %v\n%s", err, b)
	}
	for _, f := range []string{"report.md", "report.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"pw-123", "\"dt\"", "\"tt\""} {
		if strings.Contains(string(b), s) {
			t.Fatalf("results.jsonl contiene un secreto: %s", b)
		}
	}
	// Sin --gate sale con 0 aunque las metas no cumplan (solo E3); con --gate, 1.
	code = run(context.Background(), []string{"run", "--cases", "../../material/pruebas/E3*.json", "--agent", "http://agent.test",
		"--out", out, "--poll", "1ms", "--gate"}, func(k string) string { return env[k] }, fixedNow, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("--gate con metas sin cumplir: codigo %d", code)
	}
	// Falta una variable de entorno: no corre.
	delete(env, "SO_DESKTOP_TOKEN")
	if code := run(context.Background(), []string{"run"}, func(k string) string { return env[k] }, fixedNow, &stdout, &stderr); code != 2 {
		t.Fatalf("sin SO_DESKTOP_TOKEN: codigo %d", code)
	}
}
