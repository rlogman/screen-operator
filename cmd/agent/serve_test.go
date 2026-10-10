package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"screenoperator/internal/tasks"
	"strings"
	"sync"
	"testing"
	"time"
)

// memListener es un net.Listener en memoria (net.Pipe): el sandbox de los tests
// no permite bindear puertos.
type memListener struct {
	conns  chan net.Conn
	once   sync.Once
	closed chan struct{}
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080} }

func (l *memListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	c, s := net.Pipe()
	select {
	case l.conns <- s:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *memListener) client() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: l.dial}}
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

func agentEnv(t *testing.T, kv map[string]string) func(string) string {
	t.Helper()
	dir := configDir(t)
	return func(k string) string {
		switch k {
		case EnvConfigDir:
			return dir
		case "SO_CONFIG_OVERLAY":
			return ""
		}
		return kv[k]
	}
}

// startAgent corre run() con un listener en memoria y devuelve la direccion
// pedida, el listener y una funcion que apaga el agente y devuelve su codigo.
func startAgent(t *testing.T, kv map[string]string) (string, *memListener, *syncBuffer, func() int) {
	t.Helper()
	ln := newMemListener()
	addrCh := make(chan string, 1)
	listen := func(network, addr string) (net.Listener, error) {
		if network != "tcp" {
			return nil, errors.New("red inesperada " + network)
		}
		addrCh <- addr
		return ln, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	codeCh := make(chan int, 1)
	go func() { codeCh <- run(ctx, nil, agentEnv(t, kv), out, out, listen) }()
	var addr string
	select {
	case addr = <-addrCh:
	case code := <-codeCh:
		cancel()
		t.Fatalf("el agente salio con %d antes de escuchar:\n%s", code, out.String())
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("el agente no abrio el listener")
	}
	stop := func() int {
		cancel()
		select {
		case c := <-codeCh:
			return c
		case <-time.After(10 * time.Second):
			t.Fatal("el agente no se detuvo")
			return -1
		}
	}
	t.Cleanup(func() { cancel() })
	return addr, ln, out, stop
}

func TestE3_1_S3_MissingTeamTokenExits(t *testing.T) {
	// En proceso: run devuelve != 0 y el mensaje nombra la variable.
	var stdout, stderr bytes.Buffer
	listen := func(string, string) (net.Listener, error) {
		t.Fatal("no debe escuchar sin TEAM_TOKEN")
		return nil, nil
	}
	code := run(context.Background(), nil, agentEnv(t, nil), &stdout, &stderr, listen)
	if code == 0 {
		t.Fatal("run salio con 0 sin TEAM_TOKEN")
	}
	if !strings.Contains(stderr.String(), "TEAM_TOKEN") {
		t.Fatalf("el mensaje no nombra TEAM_TOKEN: %q", stderr.String())
	}

	// Proceso real: el binario termina con codigo distinto de 0.
	bin := buildAgent(t)
	code2, out := runAgent(t, bin, []string{EnvConfigDir + "=" + configDir(t), "SO_CONFIG_OVERLAY=", "TEAM_TOKEN=", "PORT=1"})
	if code2 == 0 {
		t.Fatalf("el binario salio con 0 sin TEAM_TOKEN:\n%s", out)
	}
	if !strings.Contains(out, "TEAM_TOKEN") {
		t.Fatalf("el binario no nombra TEAM_TOKEN:\n%s", out)
	}
}

func TestE3_1_S4_ListensOnPortDefault8080(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"PORT=8080", map[string]string{"TEAM_TOKEN": "t0k", "PORT": "8080"}, ":8080"},
		{"sin PORT", map[string]string{"TEAM_TOKEN": "t0k"}, ":8080"},
		{"PORT=9090", map[string]string{"TEAM_TOKEN": "t0k", "PORT": "9090"}, ":9090"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, ln, out, stop := startAgent(t, tc.env)
			if addr != tc.want {
				t.Fatalf("escucha en %q, se esperaba %q", addr, tc.want)
			}
			// El listener abierto sirve el contrato.
			resp, err := ln.client().Get("http://agent/health")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("/health = %d %s", resp.StatusCode, body)
			}
			if code := stop(); code != 0 {
				t.Fatalf("apagado con codigo %d:\n%s", code, out.String())
			}
		})
	}
	// Un PORT invalido no arranca.
	var stderr bytes.Buffer
	code := run(context.Background(), nil, agentEnv(t, map[string]string{"TEAM_TOKEN": "t0k", "PORT": "ochenta"}), io.Discard, &stderr,
		func(string, string) (net.Listener, error) { t.Fatal("no debe escuchar"); return nil, nil })
	if code == 0 || !strings.Contains(stderr.String(), "PORT") {
		t.Fatalf("PORT invalido: codigo %d, %q", code, stderr.String())
	}
}

// El agente real esta conectado: una tarea contra un escritorio que no responde
// termina fallida con el resumen de conexion (el runner provisional ya no existe).
func TestAgentRealRunnerFailsUnreachableDesktop(t *testing.T) {
	_, ln, _, stop := startAgent(t, map[string]string{"TEAM_TOKEN": "t0k"})
	defer stop()
	c := ln.client()
	req, _ := http.NewRequest(http.MethodPost, "http://agent/tasks",
		strings.NewReader(`{"tarea":"x","escritorio":{"url":"ws://127.0.0.1:1/websockify","password":"Pw-serve-77"}}`))
	req.Header.Set("Authorization", "Bearer t0k")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != 200 || created["task_id"] == "" {
		t.Fatalf("POST /tasks = %d %v", resp.StatusCode, created)
	}
	deadline := time.Now().Add(15 * time.Second) // el adaptador reintenta la conexion (2 esperas de 3 s)
	for {
		g, _ := http.NewRequest(http.MethodGet, "http://agent/tasks/"+created["task_id"], nil)
		g.Header.Set("Authorization", "Bearer t0k")
		resp, err := c.Do(g)
		if err != nil {
			t.Fatal(err)
		}
		var st map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if st["estado"] == "fallida" {
			if st["resumen"] != tasks.SummaryDialFail {
				t.Fatalf("resumen = %q", st["resumen"])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("la tarea no termino: %v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
