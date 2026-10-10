//go:build desktop

package rfb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Escenarios contra el escritorio real. Leen SO_DESKTOP_URL y SO_DESKTOP_PASSWORD
// del entorno; sin ellos el test se salta. Ningun valor se imprime. Se corren
// solo con `make test-desktop` (-p 1: todos los paquetes comparten el escritorio).
func desktopEnv(t *testing.T) (string, string) {
	t.Helper()
	u, p := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD")
	if u == "" || p == "" {
		t.Skip("escritorio real no configurado: faltan SO_DESKTOP_URL y/o SO_DESKTOP_PASSWORD en el entorno")
	}
	return u, p
}

func TestE2_2_S5_EscritorioReal1280x800(t *testing.T) {
	u, p := desktopEnv(t)
	sess, err := Dial(context.Background(), u, Options{Password: p, Shared: true, ConnectTimeout: 15 * time.Second})
	if err != nil {
		if strings.Contains(err.Error(), p) {
			t.Fatal("el error de conexion contiene la contrasena")
		}
		t.Fatalf("Dial: %v", err)
	}
	defer sess.Close()
	if w, h := sess.Size(); w != 1280 || h != 800 {
		t.Fatalf("tamano %dx%d, se esperaba 1280x800", w, h)
	}
}
