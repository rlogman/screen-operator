package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/fakes/rfbserver"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func probeRun(t *testing.T, vars map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"-config", "../../config", "-timeout", "5s"}, args...), env(vars), &out, &errb)
	return code, out.String(), errb.String()
}

func TestE2_7_S3_ErrorDeConexionSinContrasena(t *testing.T) {
	srv := rfbserver.Start(rfbserver.Options{Password: "secreta1"})
	defer srv.Close()
	cases := []struct {
		name string
		vars map[string]string
	}{
		{"auth fallida", map[string]string{"SO_DESKTOP_URL": srv.URL(), "SO_DESKTOP_PASSWORD": "Clave-Equivocada-77"}},
		{"puerto cerrado", map[string]string{"SO_DESKTOP_URL": "ws://127.0.0.1:1/websockify", "SO_DESKTOP_PASSWORD": "Clave-Equivocada-77"}},
		// La contrasena tambien en la query de la URL: el error no debe repetirla.
		{"secreto en la URL", map[string]string{"SO_DESKTOP_URL": "ws://127.0.0.1:1/websockify?password=Clave-Equivocada-77", "SO_DESKTOP_PASSWORD": "Clave-Equivocada-77"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := probeRun(t, c.vars, "shot", filepath.Join(t.TempDir(), "x.png"))
			if code == 0 {
				t.Fatalf("rfbprobe salio con 0 ante una conexion fallida")
			}
			if errOut == "" {
				t.Fatal("sin mensaje de error")
			}
			if strings.Contains(errOut+out, "Clave-Equivocada-77") {
				t.Fatalf("la salida contiene la contrasena: %q", errOut)
			}
		})
	}
}

func TestShotYClickContraServidorFalso(t *testing.T) {
	srv := rfbserver.Start(rfbserver.Options{Password: "secreta1", RepaintDelay: 300 * time.Millisecond, MinClickHold: 100 * time.Millisecond})
	defer srv.Close()
	green := color.RGBA{0, 180, 0, 255}
	srv.OnClick(func(s *rfbserver.Server, c rfbserver.Click) { s.FillRect(image.Rect(0, 0, 1280, 400), green) })
	out := filepath.Join(t.TempDir(), "out.png")
	code, stdout, stderr := probeRun(t, map[string]string{"SO_DESKTOP_URL": srv.URL(), "SO_DESKTOP_PASSWORD": "secreta1"},
		"click", "268", "160", "shot", out)
	if code != 0 {
		t.Fatalf("codigo %d: %s %s", code, stdout, stderr)
	}
	img := decodePNG(t, out)
	if b := img.Bounds(); b.Dx() != 1280 || b.Dy() != 800 {
		t.Fatalf("captura de %v", b)
	}
	if r, g, b, _ := img.At(268, 160).RGBA(); r>>8 != 0 || g>>8 != 180 || b>>8 != 0 {
		t.Fatal("la captura no es posterior al repintado del clic")
	}
	if cl := srv.Clicks(); len(cl) != 1 || cl[0].X != 268 || cl[0].Y != 160 {
		t.Fatalf("clics %+v", cl)
	}
}

func TestResetLlamaPOSTConBearer(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotPath = r.Header.Get("Authorization"), r.Method, r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer hs.Close()
	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http") + "/websockify"
	code, stdout, stderr := probeRun(t, map[string]string{"SO_DESKTOP_URL": wsURL, "SO_DESKTOP_TOKEN": "tok-123"}, "reset")
	if code != 0 {
		t.Fatalf("codigo %d: %s", code, stderr)
	}
	if gotMethod != http.MethodPost || gotPath != "/__reset" || gotAuth != "Bearer tok-123" {
		t.Fatalf("peticion %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
	if !strings.Contains(stdout, "HTTP 202") || strings.Contains(stdout+stderr, "tok-123") {
		t.Fatalf("salida %q", stdout)
	}
}

func TestComandosInvalidos(t *testing.T) {
	vars := map[string]string{"SO_DESKTOP_URL": "ws://127.0.0.1:1/websockify"}
	for _, args := range [][]string{{}, {"volar"}, {"click", "1"}} {
		if code, _, _ := probeRun(t, vars, args...); code != 2 {
			t.Fatalf("%v: codigo %d, se esperaba 2", args, code)
		}
	}
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("%s no es un PNG valido: %v", path, err)
	}
	return img
}
