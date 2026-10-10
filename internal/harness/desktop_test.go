//go:build desktop

package harness

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// desktopEnv lee las variables del escritorio real o salta el test.
func desktopEnv(t *testing.T) (base, ws, password, token string) {
	t.Helper()
	raw, password, token := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD"), os.Getenv("SO_DESKTOP_TOKEN")
	if raw == "" || password == "" || token == "" {
		t.Skip("ENVIRONMENTAL: faltan SO_DESKTOP_URL, SO_DESKTOP_PASSWORD o SO_DESKTOP_TOKEN (cargar .env)")
	}
	base, ws, err := DesktopBase(raw)
	if err != nil {
		t.Fatalf("SO_DESKTOP_URL: %v", err)
	}
	return base, ws, password, token
}

// localAgent usa SO_AGENT_URL si esta definida; si no, compila cmd/agent y lo
// levanta en 127.0.0.1 con TEAM_TOKEN del entorno.
func localAgent(t *testing.T) (url, token string) {
	t.Helper()
	token = os.Getenv("TEAM_TOKEN")
	if token == "" {
		t.Skip("ENVIRONMENTAL: falta TEAM_TOKEN (cargar .env)")
	}
	if u := os.Getenv("SO_AGENT_URL"); u != "" {
		return strings.TrimRight(u, "/"), token
	}
	bin := filepath.Join(t.TempDir(), "agent")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/agent")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build cmd/agent: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("ENVIRONMENTAL: no se puede bindear loopback (sandbox): %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	traceDir := t.TempDir()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), "SO_CONFIG_DIR=../../config", "SO_CONFIG_OVERLAY=", "SO_TRACE_DIR="+traceDir)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
		// Los logs del agente nunca llevan secretos (redaccion de la epica 3).
		for _, s := range []string{os.Getenv("SO_DESKTOP_PASSWORD"), token} {
			if s != "" && strings.Contains(logs.String(), s) {
				t.Errorf("los logs del agente contienen un secreto")
			}
		}
	})
	url = fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(url + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return url, token
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("el agente no respondio /health:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestE5_1_S5_DesktopRunRecordsRealCheck(t *testing.T) {
	base, ws, password, dtoken := desktopEnv(t)
	agentURL, atoken := localAgent(t)
	c := officialCase(t, "E3-facturas-vencidas.json")

	var out bytes.Buffer
	r := &Runner{
		Desktop:   &Desktop{BaseURL: base, Token: dtoken},
		Agent:     &Agent{BaseURL: agentURL, Token: atoken},
		DesktopWS: ws,
		Password:  password,
		Secrets:   []string{atoken, dtoken, os.Getenv("OPENROUTER_API_KEY")},
		Out:       &out,
		Log:       func(f string, a ...any) { t.Logf(f, a...) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	recs, err := r.Run(ctx, []Case{c})
	// Dejar el escritorio limpio.
	defer func() {
		if err := r.Desktop.Reset(context.Background()); err != nil {
			t.Logf("reset final: %v", err)
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("registros = %d", len(recs))
	}
	rec := recs[0]
	if rec.Check == nil {
		t.Fatalf("el JSONL no tiene resultado de /__check: check_error=%q error=%q", rec.CheckError, rec.Error)
	}
	if len(rec.Check.Checks) == 0 {
		t.Fatalf("/__check sin checks: %+v", rec.Check)
	}
	lines := decodeJSONL(t, out.String())
	if len(lines) != 1 || lines[0]["check"] == nil {
		t.Fatalf("JSONL = %s", out.String())
	}
	for _, s := range []string{password, atoken, dtoken} {
		if strings.Contains(out.String(), s) {
			t.Fatal("el JSONL contiene un secreto")
		}
	}
	t.Logf("E3 real: estado=%s check=%+v metricas=%+v", rec.Estado, rec.Check, rec.Metricas)
}
