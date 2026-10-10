//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Escenarios contra el escritorio real. Leen SO_DESKTOP_URL, SO_DESKTOP_PASSWORD
// y SO_DESKTOP_TOKEN del entorno; sin ellos el test se salta. Las capturas van a
// t.TempDir(), nunca a testdata. Se corren solo con `make test-desktop` (-p 1:
// todos los paquetes comparten el escritorio y en paralelo se pisan).
func desktopVars(t *testing.T, keys ...string) map[string]string {
	t.Helper()
	m := map[string]string{}
	var missing []string
	for _, k := range keys {
		v := os.Getenv(k)
		if v == "" {
			missing = append(missing, k)
		}
		m[k] = v
	}
	if len(missing) > 0 {
		t.Skipf("escritorio real no configurado: faltan %s en el entorno", strings.Join(missing, ", "))
	}
	return m
}

func assertNoSecrets(t *testing.T, vars map[string]string, text string) {
	t.Helper()
	for _, k := range []string{"SO_DESKTOP_PASSWORD", "SO_DESKTOP_TOKEN"} {
		if v := vars[k]; v != "" && strings.Contains(text, v) {
			t.Fatalf("la salida de rfbprobe contiene %s", k)
		}
	}
}

func TestE2_7_S1_ShotEscritorioReal(t *testing.T) {
	vars := desktopVars(t, "SO_DESKTOP_URL", "SO_DESKTOP_PASSWORD")
	out := filepath.Join(t.TempDir(), "out.png")
	code, stdout, stderr := probeRun(t, vars, "-timeout", "15s", "shot", out)
	assertNoSecrets(t, vars, stdout+stderr)
	if code != 0 {
		t.Fatalf("codigo %d: %s", code, stderr)
	}
	if b := decodePNG(t, out).Bounds(); b.Dx() != 1280 || b.Dy() != 800 {
		t.Fatalf("captura de %v, se esperaba 1280x800", b)
	}
}

func TestE2_7_S2_ClickYShotEscritorioReal(t *testing.T) {
	vars := desktopVars(t, "SO_DESKTOP_URL", "SO_DESKTOP_PASSWORD")
	out := filepath.Join(t.TempDir(), "out.png")
	code, stdout, stderr := probeRun(t, vars, "-timeout", "15s", "click", "268", "160", "shot", out)
	assertNoSecrets(t, vars, stdout+stderr)
	if code != 0 {
		t.Fatalf("codigo %d: %s", code, stderr)
	}
	// El clic informa la espera de estabilidad antes de la captura.
	if !regexp.MustCompile(`click \(268,160\): estable=\w+ cambio=\w+ espera=\S+ total=\S+`).MatchString(stdout) {
		t.Fatalf("salida sin el informe del clic: %q", stdout)
	}
	if i, j := strings.Index(stdout, "click (268,160)"), strings.Index(stdout, "shot: "); i < 0 || j < i {
		t.Fatalf("la captura no es posterior al clic: %q", stdout)
	}
	if b := decodePNG(t, out).Bounds(); b.Dx() != 1280 || b.Dy() != 800 {
		t.Fatalf("captura de %v", b)
	}
}

func TestE2_7_S4_ResetEscritorioReal(t *testing.T) {
	vars := desktopVars(t, "SO_DESKTOP_URL", "SO_DESKTOP_TOKEN")
	code, stdout, stderr := probeRun(t, vars, "reset")
	assertNoSecrets(t, vars, stdout+stderr)
	if !regexp.MustCompile(`reset: HTTP \d{3}`).MatchString(stdout) {
		t.Fatalf("no se imprimio el codigo HTTP: %q %q", stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("reset respondio un codigo no 2xx: %s", stdout)
	}
}
