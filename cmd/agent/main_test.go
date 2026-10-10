package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildAgent compila este comando en un directorio temporal y devuelve la ruta del binario.
func buildAgent(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func configDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs("../../config")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func runAgent(t *testing.T, bin string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err == nil {
		return 0, buf.String()
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("ejecutar agent: %v", err)
	}
	return ee.ExitCode(), buf.String()
}

func TestE1_2_S6_AgentExitsOnMissingOverlay(t *testing.T) {
	bin := buildAgent(t)
	dir := configDir(t)
	missing := filepath.Join(t.TempDir(), "overlay-que-no-existe.yaml")

	for _, args := range [][]string{nil, {"-check-config"}} {
		code, out := runAgent(t, bin, []string{EnvConfigDir + "=" + dir, "SO_CONFIG_OVERLAY=" + missing}, args...)
		if code == 0 {
			t.Fatalf("args %v: el agente salio con 0 con overlay inexistente:\n%s", args, out)
		}
		if !strings.Contains(out, missing) {
			t.Fatalf("args %v: el mensaje no nombra la ruta %s:\n%s", args, missing, out)
		}
	}

	// Control: sin overlay la configuracion del repositorio es valida.
	code, out := runAgent(t, bin, []string{EnvConfigDir + "=" + dir, "SO_CONFIG_OVERLAY="}, "-check-config")
	if code != 0 {
		t.Fatalf("con la configuracion del repo el agente deberia validar (codigo %d):\n%s", code, out)
	}
}

func TestAgentRejectsInvalidOverlay(t *testing.T) {
	ov := filepath.Join(t.TempDir(), "ov.yaml")
	if err := os.WriteFile(ov, []byte("surface:\n  input:\n    clik_hold_ms: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case EnvConfigDir:
			return configDir(t)
		case "SO_CONFIG_OVERLAY":
			return ov
		}
		return ""
	}
	if code := run(context.Background(), []string{"-check-config"}, getenv, &stdout, &stderr, nil); code == 0 {
		t.Fatal("se esperaba codigo != 0")
	}
	if !strings.Contains(stderr.String(), "surface.input.clik_hold_ms") {
		t.Fatalf("stderr no nombra la ruta: %s", stderr.String())
	}
}
