package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"screenoperator/internal/tasks"
	"strings"
	"testing"
	"time"
)

// submitAndWait crea una tarea en el agente en memoria y espera su estado final.
func submitAndWait(t *testing.T, ln *memListener) string {
	t.Helper()
	c := ln.client()
	req, _ := http.NewRequest(http.MethodPost, "http://agent/tasks",
		strings.NewReader(`{"tarea":"x","escritorio":{"url":"ws://127.0.0.1:1/websockify","password":"Pw-trace-91"}}`))
	req.Header.Set("Authorization", "Bearer t0k")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	id := created["task_id"]
	deadline := time.Now().Add(5 * time.Second)
	for {
		g, _ := http.NewRequest(http.MethodGet, "http://agent/tasks/"+id, nil)
		g.Header.Set("Authorization", "Bearer t0k")
		resp, err := c.Do(g)
		if err != nil {
			t.Fatal(err)
		}
		var st map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if st["estado"] != "en_curso" {
			return id
		}
		if time.Now().After(deadline) {
			t.Fatal("la tarea no termino")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// El agente real conecta SO_TRACE_DIR al Task Manager: una tarea cuyo escritorio
// no responde deja el paso de conexion fallida en traces/<task_id>/steps.jsonl y
// en /tasks/{id}/metrics (antes lo dejaba el runner provisional, retirado en la
// epica 6).
func TestE5_6_S1_AgentWritesTraceWithTraceDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "traces")
	_, ln, _, stop := startAgent(t, map[string]string{"TEAM_TOKEN": "t0k", "SO_TRACE_DIR": dir})
	defer stop()
	id := submitAndWait(t, ln)
	b, err := os.ReadFile(filepath.Join(dir, id, "steps.jsonl"))
	if err != nil {
		t.Fatalf("falta steps.jsonl: %v", err)
	}
	if strings.Count(string(b), "\n") != 1 || !strings.Contains(string(b), tasks.SummaryDialFail) {
		t.Fatalf("steps.jsonl = %s", b)
	}
	if strings.Contains(string(b), "Pw-trace-91") || strings.Contains(string(b), "t0k") {
		t.Fatal("la traza contiene un secreto")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://agent/tasks/"+id+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer t0k")
	resp, err := ln.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if resp.StatusCode != 200 || m["pasos"] != float64(1) {
		t.Fatalf("metrics = %d %v", resp.StatusCode, m)
	}
}

func TestE5_6_S3_AgentWithoutTraceDirWritesNothing(t *testing.T) {
	// El entorno (con la ruta absoluta de config/) se arma antes de cambiar de directorio.
	getenv := agentEnv(t, map[string]string{"TEAM_TOKEN": "t0k"})
	cwd := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	ln := newMemListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, nil, getenv, io.Discard, io.Discard, func(string, string) (net.Listener, error) { return ln, nil })
	}()
	defer func() { cancel(); <-done }()
	submitAndWait(t, ln)
	if es, _ := os.ReadDir(cwd); len(es) != 0 {
		t.Fatalf("sin SO_TRACE_DIR el agente escribio en disco: %v", es)
	}
}
