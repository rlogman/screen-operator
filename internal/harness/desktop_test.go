//go:build desktop

package harness

import (
	"bytes"
	"context"
	"errors"
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

// loadEnvFile carga KEY=VALUE de SO_ENV_FILE (si esta definida) sin pisar
// variables ya presentes y sin imprimir valores. Alternativa a
// `set -a; . .env; set +a` donde el shell no permite hacer source.
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
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			t.Setenv(k, v)
		}
	}
}

// desktopEnv lee las variables del escritorio real o salta el test.
func desktopEnv(t *testing.T) (base, ws, password, token string) {
	t.Helper()
	loadEnvFile(t)
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

// probeCase es una prueba propia cuyo diff_esperado el estado inicial no cumple.
const probeCase = `{"id":"SO-PROBE-1","nivel":"facil","categoria":"robustez","instruccion":"Prueba propia del harness: cambia la dirección de CL-0007.","estado_esperado":"terminada","diff_esperado":[{"entidad":"clientes","id":"CL-0007","campo":"direccion","accion":"update","valor":"Calle 1 # 1-01 SO-PROBE"}],"lista_blanca":[]}`

func TestE5_4_S1_DesktopCheckAcceptsOwnCase(t *testing.T) {
	base, _, _, dtoken := desktopEnv(t)
	d := &Desktop{BaseURL: base, Token: dtoken}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.Reset(ctx); err != nil {
		t.Logf("reset fallo, reintento: %v", err)
		if err := d.Reset(ctx); err != nil {
			t.Fatalf("ENVIRONMENTAL: /__reset: %v", err)
		}
	}
	defer d.Reset(context.Background())

	res, err := d.Check(ctx, []byte(probeCase))
	if err == nil {
		t.Logf("/__check acepto la prueba propia: pass=%v checks=%+v", res.Pass, res.Checks)
		if res.Pass || len(res.Checks) == 0 {
			t.Fatalf("tras un reset, un diff_esperado no cumplido deberia dar pass=false con checks: %+v", res)
		}
	} else {
		t.Logf("/__check rechazo la prueba propia: %v (sin check de estado: %v)", err, errors.Is(err, ErrCheckUnsupported))
	}

	// Informativo: que devuelve /__check ante una prueba que no entiende.
	for _, body := range []string{`{}`, `{"id":"SO-PROBE-2","estado_esperado":"terminada","diff_esperado":[{"entidad":"no_existe","id":"X-1","campo":"y","accion":"update","valor":"z"}],"lista_blanca":[]}`} {
		r2, e2 := d.Check(ctx, []byte(body))
		t.Logf("/__check con %.40s...: resultado=%+v error=%v", body, r2, e2)
	}

	// El resultado queda registrado en docs/assumptions.md.
	doc, rerr := os.ReadFile(filepath.Join("..", "..", "docs", "assumptions.md"))
	if rerr != nil {
		t.Fatalf("falta docs/assumptions.md: %v", rerr)
	}
	want := "acepta pruebas propias: sí"
	if err != nil {
		want = "acepta pruebas propias: no"
	}
	if !strings.Contains(string(doc), "[E5.4-S1]") || !strings.Contains(string(doc), want) {
		t.Fatalf("docs/assumptions.md no registra el resultado observado (%q)", want)
	}
}
