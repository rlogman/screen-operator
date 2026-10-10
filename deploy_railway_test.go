package screenoperator_test

// Tests de la historia 4.2: railway.json, docs/deploy.md y
// scripts/check_entrypoint.sh.
//
// check_entrypoint.sh se prueba de dos formas:
//   - Con un curl falso (este mismo binario de test re-ejecutado, ver TestMain)
//     que atiende las peticiones con un handler en memoria. Corre siempre: el
//     sandbox de los tests no deja abrir sockets (EPERM, ENVIRONMENTAL).
//   - Con curl real contra el binario de cmd/agent escuchando en 127.0.0.1.
//     Se salta con "ENVIRONMENTAL" si el bind da EPERM.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

const fakeCurlEnv = "SO_E4_FAKE_CURL"

func TestMain(m *testing.M) {
	if os.Getenv(fakeCurlEnv) == "1" {
		os.Exit(fakeCurl(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// ---------- E4.2-S2: railway.json ----------

func TestE4_2_S2_RailwayJSONDeclaraDockerfileYHealth(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "railway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("railway.json no parsea: %v", err)
	}
	// Claves permitidas segun https://railway.com/railway.schema.json (consultado 2026-10-10).
	allowedTop := map[string]bool{"$schema": true, "build": true, "deploy": true}
	for k := range cfg {
		if !allowedTop[k] {
			t.Errorf("clave de primer nivel desconocida en railway.json: %q", k)
		}
	}
	var schema string
	_ = json.Unmarshal(cfg["$schema"], &schema)
	if schema != "https://railway.com/railway.schema.json" {
		t.Errorf("$schema = %q", schema)
	}
	var build struct {
		Builder        string `json:"builder"`
		DockerfilePath string `json:"dockerfilePath"`
	}
	var deploy struct {
		HealthcheckPath         string `json:"healthcheckPath"`
		HealthcheckTimeout      int    `json:"healthcheckTimeout"`
		RestartPolicyType       string `json:"restartPolicyType"`
		RestartPolicyMaxRetries int    `json:"restartPolicyMaxRetries"`
	}
	strict := func(name string, msg json.RawMessage, v any) {
		d := json.NewDecoder(bytes.NewReader(msg))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			t.Fatalf("railway.json %s: %v", name, err)
		}
	}
	strict("build", cfg["build"], &build)
	strict("deploy", cfg["deploy"], &deploy)
	if build.Builder != "DOCKERFILE" {
		t.Errorf("build.builder = %q, se esperaba DOCKERFILE", build.Builder)
	}
	if build.DockerfilePath != "Dockerfile" {
		t.Errorf("build.dockerfilePath = %q, se esperaba Dockerfile", build.DockerfilePath)
	}
	if _, err := os.Stat(filepath.Join(repoRoot(t), build.DockerfilePath)); err != nil {
		t.Errorf("dockerfilePath no existe: %v", err)
	}
	if deploy.HealthcheckPath != "/health" {
		t.Errorf("deploy.healthcheckPath = %q, se esperaba /health", deploy.HealthcheckPath)
	}
	// Debe coincidir con la ruta de health del contrato.
	base, err := os.ReadFile(filepath.Join(repoRoot(t), "config", "base.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`health:\s*\{\s*method:\s*GET,\s*path:\s*/health\s*\}`).Match(base) {
		t.Error("config/base.yaml ya no declara GET /health: railway.json quedo desalineado")
	}
	if deploy.HealthcheckTimeout <= 0 || deploy.HealthcheckTimeout > 300 {
		t.Errorf("deploy.healthcheckTimeout = %d fuera de (0, 300]", deploy.HealthcheckTimeout)
	}
	switch deploy.RestartPolicyType {
	case "ON_FAILURE", "ALWAYS", "NEVER":
	default:
		t.Errorf("deploy.restartPolicyType = %q no es un valor del esquema", deploy.RestartPolicyType)
	}
}

// ---------- E4.2-S1: docs/deploy.md ----------

func TestE4_2_S1_RunbookTieneLasSecciones(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "deploy.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	sections := map[string]string{}
	var cur string
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "## ") {
			cur = strings.ToLower(strings.TrimPrefix(l, "## "))
			continue
		}
		sections[cur] += l + "\n"
	}
	find := func(title string) string {
		for k, v := range sections {
			if strings.Contains(k, title) {
				return v
			}
		}
		t.Fatalf("docs/deploy.md no tiene una seccion '## ...%s...'", title)
		return ""
	}
	checks := []struct {
		section string
		must    []string
	}{
		{"crear el servicio", []string{"New Project", "GitHub repo", "Dockerfile"}},
		{"variables", []string{"TEAM_TOKEN", "OPENROUTER_API_KEY", "SO_CONFIG_OVERLAY", "opcional", "No |"}},
		{"healthcheck", []string{"/health", "railway.json", "healthcheckPath"}},
		{"logs", []string{"Deploy Logs", "Build Logs"}},
		{"redeploy", []string{"Deployments", "Deploy Latest Commit", "ollback"}},
		{"validar el entrypoint", []string{"scripts/check_entrypoint.sh", "VALIDAR ENTRYPOINT", "/health", "POST /tasks", "GET /tasks/{task_id}"}},
	}
	for _, c := range checks {
		body := find(c.section)
		for _, m := range c.must {
			if !strings.Contains(body, m) {
				t.Errorf("la seccion %q no menciona %q", c.section, m)
			}
		}
	}
	// SO_CONFIG_OVERLAY figura como opcional en la tabla de variables.
	if !regexp.MustCompile("(?m)^\\| `SO_CONFIG_OVERLAY` \\| No \\|").MatchString(doc) {
		t.Error("la tabla de variables no marca SO_CONFIG_OVERLAY como no obligatoria")
	}
	// Cita la documentacion oficial de Railway consultada.
	for _, u := range []string{"https://docs.railway.com/reference/config-as-code", "https://docs.railway.com/reference/healthchecks"} {
		if !strings.Contains(doc, u) {
			t.Errorf("docs/deploy.md no cita %s", u)
		}
	}
}

// ---------- E4.2-S3: scripts/check_entrypoint.sh ----------

// fakeMode controla como responde el servidor falso.
const (
	modeOK         = "ok"
	modeHealth503  = "health503"
	modeSlowPost   = "slowpost"
	modeNoResumen  = "noresumen"
	modeNoAuth     = "noauth"
	modeBadEstado  = "badestado"
	modeNoTaskID   = "notaskid"
	fakeTaskID     = "0a1b2c3d4e5f"
	fakeTokenEnv   = "SO_E4_FAKE_TOKEN"
	fakeModeEnv    = "SO_E4_FAKE_MODE"
	fakeSlowPostMs = 2200
)

func fakeHandler(mode, token string) http.Handler {
	mux := http.NewServeMux()
	authed := func(r *http.Request) bool {
		if mode == modeNoAuth {
			return true
		}
		return r.Header.Get("Authorization") == "Bearer "+token
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if mode == modeHealth503 {
			http.Error(w, "no listo", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"no autorizado"}`)
			return
		}
		var body struct {
			Tarea      string `json:"tarea"`
			Escritorio struct {
				URL      string `json:"url"`
				Password string `json:"password"`
			} `json:"escritorio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Tarea == "" || body.Escritorio.URL == "" {
			http.Error(w, `{"error":"cuerpo invalido"}`, http.StatusBadRequest)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, `{"error":"content-type"}`, http.StatusBadRequest)
			return
		}
		if mode == modeSlowPost {
			time.Sleep(fakeSlowPostMs * time.Millisecond)
		}
		if mode == modeNoTaskID {
			io.WriteString(w, `{"id":"x"}`)
			return
		}
		fmt.Fprintf(w, `{"task_id":%q}`, fakeTaskID)
	})
	mux.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.PathValue("id") != fakeTaskID {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"estado":"fallida","resumen":"Tarea desconocida para este agente."}`)
			return
		}
		switch mode {
		case modeNoResumen:
			io.WriteString(w, `{"estado":"fallida"}`)
		case modeBadEstado:
			io.WriteString(w, `{"estado":"ok","resumen":"x"}`)
		default:
			io.WriteString(w, `{"estado":"fallida","resumen":"agente en construcción"}`)
		}
	})
	return mux
}

// fakeCurl implementa el subconjunto de curl que usa check_entrypoint.sh y
// atiende la peticion con fakeHandler en memoria.
func fakeCurl(args []string) int {
	var (
		out, wfmt, method, url string
		headers                []string
		body                   []byte
	)
	method = http.MethodGet
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "fake curl: falta valor para", a)
				os.Exit(2)
			}
			return args[i]
		}
		switch a {
		case "-sS", "-s", "-S":
		case "--max-time":
			next()
		case "-o":
			out = next()
		case "-w":
			wfmt = next()
		case "-X":
			method = next()
		case "-H":
			h := next()
			if strings.HasPrefix(h, "@") {
				raw, err := os.ReadFile(h[1:])
				if err != nil {
					fmt.Fprintln(os.Stderr, "fake curl:", err)
					return 26
				}
				for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
					headers = append(headers, l)
				}
			} else {
				headers = append(headers, h)
			}
		case "--data-binary":
			d := next()
			if strings.HasPrefix(d, "@") {
				raw, err := os.ReadFile(d[1:])
				if err != nil {
					fmt.Fprintln(os.Stderr, "fake curl:", err)
					return 26
				}
				body = raw
			} else {
				body = []byte(d)
			}
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintln(os.Stderr, "fake curl: opcion no soportada", a)
				return 2
			}
			url = a
		}
	}
	start := time.Now()
	req := httptest.NewRequest(method, url, bytes.NewReader(body))
	for _, h := range headers {
		k, v, _ := strings.Cut(h, ":")
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	rec := httptest.NewRecorder()
	fakeHandler(os.Getenv(fakeModeEnv), os.Getenv(fakeTokenEnv)).ServeHTTP(rec, req)
	elapsed := time.Since(start).Seconds()
	if out != "" {
		if err := os.WriteFile(out, rec.Body.Bytes(), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "fake curl:", err)
			return 23
		}
	}
	w := strings.ReplaceAll(wfmt, "%{http_code}", fmt.Sprintf("%03d", rec.Code))
	w = strings.ReplaceAll(w, "%{time_total}", fmt.Sprintf("%.6f", elapsed))
	fmt.Print(w)
	return 0
}

func runCheckScript(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	script := filepath.Join(repoRoot(t), "scripts", "check_entrypoint.sh")
	cmd := exec.Command(script, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, "TMPDIR="+t.TempDir())
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err == nil {
		return 0, buf.String()
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("ejecutar check_entrypoint.sh: %v", err)
	}
	return ee.ExitCode(), buf.String()
}

func TestE4_2_S3_CheckEntrypointContraServidorFalso(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const token = "tok-s3cr3t-e4"
	cases := []struct {
		mode     string
		wantCode int
		want     []string
	}{
		{modeOK, 0, []string{"OK    GET /health -> 200", "OK    POST /tasks sin token -> 401", "OK    POST /tasks -> 200 task_id=" + fakeTaskID, "OK    GET /tasks/" + fakeTaskID + " -> estado=fallida", "RESULTADO: entrypoint OK"}},
		{modeHealth503, 1, []string{"FALLA GET /health -> 503"}},
		{modeSlowPost, 1, []string{"FALLA POST /tasks tardo"}},
		{modeNoResumen, 1, []string{`sin "resumen"`}},
		{modeNoAuth, 1, []string{"FALLA POST /tasks sin token -> 200"}},
		{modeBadEstado, 1, []string{`estado="ok"`}},
		{modeNoTaskID, 1, []string{"sin task_id", "FALLA GET /tasks/{task_id} no se puede probar"}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			env := []string{"CURL=" + self, fakeCurlEnv + "=1", fakeModeEnv + "=" + tc.mode, fakeTokenEnv + "=" + token}
			code, out := runCheckScript(t, env, "https://agente.example", token)
			if code != tc.wantCode {
				t.Fatalf("codigo %d, se esperaba %d:\n%s", code, tc.wantCode, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("la salida no contiene %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, token) {
				t.Fatalf("la salida imprime el token:\n%s", out)
			}
		})
	}
	// Token equivocado: el servidor responde 401 a todo lo autenticado.
	env := []string{"CURL=" + self, fakeCurlEnv + "=1", fakeModeEnv + "=" + modeOK, fakeTokenEnv + "=" + token}
	if code, out := runCheckScript(t, env, "https://agente.example", "otro"); code != 1 || !strings.Contains(out, "FALLA POST /tasks -> 401") {
		t.Fatalf("token equivocado: codigo %d:\n%s", code, out)
	}
	// Uso incorrecto.
	for _, args := range [][]string{{}, {"https://agente.example"}, {"agente.example", "t"}, {"https://agente.example", ""}} {
		if code, out := runCheckScript(t, nil, args...); code != 2 {
			t.Errorf("args %q: codigo %d, se esperaba 2:\n%s", args, code, out)
		}
	}
}

// freePort abre y cierra un listener en 127.0.0.1. Si el sandbox lo prohibe
// (EPERM), el test se salta como ENVIRONMENTAL.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("ENVIRONMENTAL: el sandbox no deja abrir puertos (%v); el script se prueba con el curl falso", err)
		}
		t.Fatal(err)
	}
	defer ln.Close()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
}

func TestE4_2_S3_CheckEntrypointContraAgenteLocal(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("ENVIRONMENTAL: no hay curl")
	}
	port := freePort(t)
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "agent")
	b := exec.Command("go", "build", "-o", bin, "./cmd/agent")
	b.Dir = root
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	const token = "tok-local-e4"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := exec.CommandContext(ctx, bin)
	agent.Env = append(os.Environ(), "SO_CONFIG_DIR="+filepath.Join(root, "config"), "SO_CONFIG_OVERLAY=", "TEAM_TOKEN="+token, "PORT="+port)
	var logs bytes.Buffer
	agent.Stdout, agent.Stderr = &logs, &logs
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = agent.Wait() })
	base := "http://127.0.0.1:" + port
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("el agente no respondio /health:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	code, out := runCheckScript(t, nil, base, token)
	if code != 0 {
		t.Fatalf("check_entrypoint.sh contra el agente local: codigo %d:\n%s\nlogs del agente:\n%s", code, out, logs.String())
	}
	for _, w := range []string{"OK    GET /health -> 200", "OK    POST /tasks sin token -> 401", "OK    POST /tasks -> 200", "estado=", "RESULTADO: entrypoint OK"} {
		if !strings.Contains(out, w) {
			t.Errorf("la salida no contiene %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, token) || strings.Contains(logs.String(), token) {
		t.Fatal("el token aparece en la salida del script o en los logs del agente")
	}
	// Con token equivocado el script falla.
	if code, out := runCheckScript(t, nil, base, "otro"); code != 1 {
		t.Fatalf("token equivocado: codigo %d:\n%s", code, out)
	}
}
