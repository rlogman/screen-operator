package main

// Tests de la historia 4.1 sin daemon de Docker: el agente arranca con el
// mismo layout y las mismas variables que fija la imagen (COPY config/ y ENV
// del Dockerfile). Los tests contra la imagen real estan en
// deploy_docker_test.go en la raiz (build tag docker).

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// imageLayout lee del Dockerfile de la etapa final el destino de COPY config/
// y las variables ENV, y replica ese layout bajo un directorio temporal.
func imageLayout(t *testing.T) (env map[string]string, configPath string) {
	t.Helper()
	raw, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	// Une continuaciones y se queda con la ultima etapa.
	var lines []string
	var cur strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if strings.HasSuffix(l, `\`) {
			cur.WriteString(strings.TrimSuffix(l, `\`) + " ")
			continue
		}
		cur.WriteString(l)
		if s := strings.TrimSpace(cur.String()); s != "" {
			if strings.HasPrefix(strings.ToUpper(s), "FROM ") {
				lines = nil
			}
			lines = append(lines, s)
		}
		cur.Reset()
	}
	env = map[string]string{}
	var dest string
	for _, l := range lines {
		cmd, args, _ := strings.Cut(l, " ")
		switch strings.ToUpper(cmd) {
		case "ENV":
			for _, kv := range strings.Fields(args) {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					t.Fatalf("ENV sin forma clave=valor: %q", kv)
				}
				env[k] = v
			}
		case "COPY":
			f := strings.Fields(args)
			if len(f) == 2 && strings.TrimSuffix(f[0], "/") == "config" {
				dest = f[1]
			}
		}
	}
	if dest == "" {
		t.Fatal("la etapa final no copia config/")
	}
	if got := strings.TrimSuffix(env[EnvConfigDir], "/"); got != strings.TrimSuffix(dest, "/") {
		t.Fatalf("ENV %s=%q no apunta al destino de COPY config/ (%q)", EnvConfigDir, got, dest)
	}
	// Replica /app/config bajo un directorio temporal.
	root := t.TempDir()
	configPath = filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(dest, "/")))
	if err := os.CopyFS(configPath, os.DirFS(configDir(t))); err != nil {
		t.Fatal(err)
	}
	return env, configPath
}

// imageEnv es el entorno del contenedor: ENV de la imagen + las variables de
// `docker run -e`, con SO_CONFIG_DIR redirigido a la copia local.
func imageEnv(t *testing.T, run map[string]string) func(string) string {
	env, cfg := imageLayout(t)
	merged := map[string]string{}
	for k, v := range env {
		merged[k] = v
	}
	merged[EnvConfigDir] = cfg
	for k, v := range run {
		merged[k] = v
	}
	return func(k string) string { return merged[k] }
}

func TestE4_1_S2_ConfigDeImagenSirveHealth(t *testing.T) {
	env, _ := imageLayout(t)
	if env[EnvPort] != "8080" {
		t.Fatalf("la imagen deberia fijar PORT=8080, tiene %q", env[EnvPort])
	}
	ln := newMemListener()
	addrCh := make(chan string, 1)
	listen := func(network, addr string) (net.Listener, error) {
		addrCh <- addr
		return ln, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	codeCh := make(chan int, 1)
	getenv := imageEnv(t, map[string]string{"TEAM_TOKEN": "t0k", "PORT": "8080"})
	go func() { codeCh <- run(ctx, nil, getenv, out, out, listen) }()
	select {
	case addr := <-addrCh:
		if addr != ":8080" {
			t.Fatalf("escucha en %q, se esperaba :8080", addr)
		}
	case code := <-codeCh:
		t.Fatalf("el agente salio con %d con la configuracion de la imagen:\n%s", code, out.String())
	case <-time.After(10 * time.Second):
		t.Fatal("el agente no abrio el listener")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://agent/health", nil)
	req.Header.Set("Authorization", "Bearer t0k")
	resp, err := ln.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health con token = %d %s", resp.StatusCode, body)
	}
	cancel()
	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("apagado con codigo %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("el agente no se detuvo")
	}
}

func TestE4_1_S3_ImagenSinTeamTokenSaleDistintoDeCero(t *testing.T) {
	env, _ := imageLayout(t)
	if _, ok := env["TEAM_TOKEN"]; ok {
		t.Fatal("la imagen no debe fijar TEAM_TOKEN")
	}
	var stderr bytes.Buffer
	listen := func(string, string) (net.Listener, error) {
		return nil, errors.New("no debe escuchar sin TEAM_TOKEN")
	}
	code := run(context.Background(), nil, imageEnv(t, map[string]string{"PORT": "8080"}), io.Discard, &stderr, listen)
	if code == 0 {
		t.Fatal("el agente salio con 0 sin TEAM_TOKEN")
	}
	if !strings.Contains(stderr.String(), "TEAM_TOKEN") {
		t.Fatalf("el mensaje no nombra TEAM_TOKEN: %q", stderr.String())
	}
	// El binario real con el mismo entorno.
	_, cfg := imageLayout(t)
	code2, out := runAgent(t, buildAgent(t), []string{EnvConfigDir + "=" + cfg, "SO_CONFIG_OVERLAY=", "TEAM_TOKEN=", "PORT=8080"})
	if code2 == 0 {
		t.Fatalf("el binario salio con 0 sin TEAM_TOKEN:\n%s", out)
	}
}
