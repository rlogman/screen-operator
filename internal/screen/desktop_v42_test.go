//go:build desktop

package screen

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"screenoperator/internal/rfb"
)

// E10.3-S1: los defaults de coordenadas de los tests de escritorio estan
// actualizados al tema v4.2 (incidente FR-0153). Recien reseteada, la app
// muestra el modal 'Novedades de la version 4.2' (velo gris sobre la pagina);
// el clic en defDismissXY lo cierra y deja la pantalla principal (fondo claro).
// TestE2_6_S5 cubre el resto del recorrido (Cartera > Facturas) con la
// referencia del encabezado regrabada sobre v4.2.
//
// Requiere SO_DESKTOP_URL, SO_DESKTOP_PASSWORD y SO_DESKTOP_TOKEN (se resetea
// antes y despues). Correr con make test-desktop dentro de scripts/with_desktop.sh.
func TestE10_3_S1_V42DismissDefaultClosesOpeningModal(t *testing.T) {
	u, p, tok := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD"), os.Getenv("SO_DESKTOP_TOKEN")
	if u == "" || p == "" || tok == "" {
		t.Skip("escritorio real no configurado: faltan SO_DESKTOP_URL, SO_DESKTOP_PASSWORD y/o SO_DESKTOP_TOKEN")
	}
	ctx := context.Background()
	reset := func() {
		if code, err := rfb.Reset(ctx, nil, u, tok); err != nil || code/100 != 2 {
			t.Logf("reset del escritorio: codigo %d, %v", code, err)
		}
	}
	reset()
	t.Cleanup(reset)
	surf, perc := kiosk(t)
	var d Driver
	var err error
	for i := 0; i < 3; i++ {
		time.Sleep(5 * time.Second)
		if d, err = Open(ctx, u, p, surf, 15*time.Second); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	ex := NewExecutor(d, surf, perc)
	luma := func(o Outcome) float64 {
		c := o.Screen.RGBAAt(640, 650)
		return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
	}
	before, err := ex.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if l := luma(before); l >= 180 {
		t.Fatalf("tras el reset se esperaba el modal de apertura de v4.2 (velo gris); luma %.0f en (640,650)", l)
	}
	var dx, dy int
	if _, err := fmt.Sscanf(defDismissXY, "%d,%d", &dx, &dy); err != nil {
		t.Fatal(err)
	}
	after, err := ex.Click(ctx, dx, dy)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Changed {
		t.Fatalf("el clic en %s (Entendido) no cambio la pantalla", defDismissXY)
	}
	if l := luma(after); l < 180 {
		t.Fatalf("tras el clic en %s la pantalla sigue velada (luma %.0f): el default no cierra el modal de v4.2", defDismissXY, l)
	}
}
