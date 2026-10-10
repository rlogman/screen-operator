//go:build desktop

package screen

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"screenoperator/internal/rfb"
)

// E2.6-S5 contra el escritorio real (SIGECOM).
//
// Entorno:
//   - SO_DESKTOP_URL, SO_DESKTOP_PASSWORD: obligatorios (sin ellos, skip).
//   - SO_DESKTOP_TOKEN: opcional; si esta, se reinicia el escritorio antes.
//   - SO_E2_6_CARTERA_XY, SO_E2_6_FACTURAS_XY ("x,y"): posicion del menu
//     'Cartera' y del item 'Facturas' en pantalla (1280x800).
//   - SO_E2_6_HEADER_RECT ("x,y,w,h"): region del encabezado de la tabla que
//     contiene 'Codigo'.
//
// Verificacion por comparacion de pixeles con testdata/e2_6_facturas_header.png.
// Para grabar la referencia por primera vez: SO_E2_6_RECORD=1; el test guarda
// el recorte del encabezado en testdata/ y la captura completa en el
// directorio temporal, para inspeccion humana, y se salta. Las coordenadas se relevan con: rfbprobe shot pantalla.png.
const refHeader = "testdata/e2_6_facturas_header.png"

func TestE2_6_S5_CarteraFacturasMuestraTablaConCodigo(t *testing.T) {
	u, p := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD")
	if u == "" || p == "" {
		t.Skip("escritorio real no configurado: faltan SO_DESKTOP_URL y/o SO_DESKTOP_PASSWORD")
	}
	var cx, cy, fx, fy, hx, hy, hw, hh int
	if _, err := fmt.Sscanf(os.Getenv("SO_E2_6_CARTERA_XY"), "%d,%d", &cx, &cy); err != nil {
		t.Skip("falta SO_E2_6_CARTERA_XY=x,y (posicion del menu Cartera; relevar con rfbprobe shot)")
	}
	if _, err := fmt.Sscanf(os.Getenv("SO_E2_6_FACTURAS_XY"), "%d,%d", &fx, &fy); err != nil {
		t.Skip("falta SO_E2_6_FACTURAS_XY=x,y (posicion del item Facturas)")
	}
	if _, err := fmt.Sscanf(os.Getenv("SO_E2_6_HEADER_RECT"), "%d,%d,%d,%d", &hx, &hy, &hw, &hh); err != nil {
		t.Skip("falta SO_E2_6_HEADER_RECT=x,y,w,h (encabezado de la tabla con 'Codigo')")
	}
	ctx := context.Background()
	if tok := os.Getenv("SO_DESKTOP_TOKEN"); tok != "" {
		code, err := rfb.Reset(ctx, nil, u, tok)
		if err != nil || code/100 != 2 {
			t.Fatalf("reset del escritorio: codigo %d, %v", code, err)
		}
		time.Sleep(2 * time.Second)
	}
	surf, perc := kiosk(t)
	d, err := Open(ctx, u, p, surf, 15*time.Second)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	ex := NewExecutor(d, surf, perc)
	if _, err := ex.Click(ctx, cx, cy); err != nil {
		t.Fatal(err)
	}
	out, err := ex.Click(ctx, fx, fy)
	if err != nil {
		t.Fatal(err)
	}
	header, err := Zoom(out.Screen, image.Rect(hx, hy, hx+hw, hy+hh), 1)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SO_E2_6_RECORD") == "1" {
		// Solo el recorte del encabezado va a testdata (sin filas de datos); la
		// captura completa queda fuera del repo, para inspeccion.
		full := filepath.Join(os.TempDir(), "e2_6_facturas_full.png")
		writePNG(t, refHeader, header)
		writePNG(t, full, out.Screen)
		t.Skipf("referencia grabada en %s (captura completa en %s); inspeccionar que muestra el encabezado 'Codigo' y volver a correr sin SO_E2_6_RECORD", refHeader, full)
	}
	f, err := os.Open(refHeader)
	if err != nil {
		t.Skipf("no hay referencia %s: grabarla con SO_E2_6_RECORD=1", refHeader)
	}
	defer f.Close()
	ref, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	refRGBA := image.NewRGBA(ref.Bounds())
	for y := ref.Bounds().Min.Y; y < ref.Bounds().Max.Y; y++ {
		for x := ref.Bounds().Min.X; x < ref.Bounds().Max.X; x++ {
			refRGBA.Set(x, y, ref.At(x, y))
		}
	}
	if d := DiffFraction(header, refRGBA); d > 0.02 {
		got := filepath.Join(t.TempDir(), "facturas_header_obtenido.png")
		writePNG(t, got, header)
		t.Fatalf("el encabezado difiere de la referencia en %.1f%% de los pixeles (captura en %s)", d*100, got)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := EncodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
