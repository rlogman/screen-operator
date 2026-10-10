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
//     'Cartera' y del item 'Facturas' en pantalla (1280x800). Opcionales:
//     por defecto, las del escritorio de SIGECOM (ver defCarteraXY).
//   - SO_E2_6_HEADER_RECT ("x,y,w,h"): region del encabezado de la tabla que
//     contiene 'Codigo'. Opcional, mismo criterio.
//   - SO_E2_6_DISMISS_XY ("x,y" o "none"): clic previo para cerrar el dialogo
//     que la app muestra al abrir. Por defecto el boton 'Entendido' del modal
//     'Novedades de la version 4.2' de SIGECOM v4.2 (incidente FR-0153); sobre
//     una pantalla sin modal el clic cae en un area vacia de la pagina de inicio.
//
// Los tests de escritorio se corren solo con `make test-desktop` (serializa
// los paquetes con -p 1: todos comparten el mismo escritorio).
//
// Verificacion por comparacion de pixeles con testdata/e2_6_facturas_header.png.
// Para regrabar la referencia: SO_E2_6_RECORD=1; el test exige que la espera
// haya visto un cambio y terminado estable y que el recorte tenga varianza
// (no fondo plano), guarda el recorte en testdata/ y la captura completa en el
// directorio temporal, para inspeccion humana, y se salta. Las coordenadas se
// relevan con: rfbprobe shot pantalla.png.
const refHeader = "testdata/e2_6_facturas_header.png"

func TestE2_6_S5_CarteraFacturasMuestraTablaConCodigo(t *testing.T) {
	u, p := os.Getenv("SO_DESKTOP_URL"), os.Getenv("SO_DESKTOP_PASSWORD")
	if u == "" || p == "" {
		t.Skip("escritorio real no configurado: faltan SO_DESKTOP_URL y/o SO_DESKTOP_PASSWORD")
	}
	var cx, cy, fx, fy, hx, hy, hw, hh int
	if _, err := fmt.Sscanf(envOr("SO_E2_6_CARTERA_XY", defCarteraXY), "%d,%d", &cx, &cy); err != nil {
		t.Fatalf("SO_E2_6_CARTERA_XY invalido (formato x,y): %v", err)
	}
	if _, err := fmt.Sscanf(envOr("SO_E2_6_FACTURAS_XY", defFacturasXY), "%d,%d", &fx, &fy); err != nil {
		t.Fatalf("SO_E2_6_FACTURAS_XY invalido (formato x,y): %v", err)
	}
	if _, err := fmt.Sscanf(envOr("SO_E2_6_HEADER_RECT", defHeaderRect), "%d,%d,%d,%d", &hx, &hy, &hw, &hh); err != nil {
		t.Fatalf("SO_E2_6_HEADER_RECT invalido (formato x,y,w,h): %v", err)
	}
	var dx, dy int
	dismiss := envOr("SO_E2_6_DISMISS_XY", defDismissXY) != "none"
	if dismiss {
		if _, err := fmt.Sscanf(envOr("SO_E2_6_DISMISS_XY", defDismissXY), "%d,%d", &dx, &dy); err != nil {
			t.Fatalf("SO_E2_6_DISMISS_XY invalido (formato x,y o none): %v", err)
		}
	}
	record := os.Getenv("SO_E2_6_RECORD") == "1"
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
	if dismiss {
		if _, err := ex.Click(ctx, dx, dy); err != nil {
			t.Fatal(err)
		}
	}
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
	if record {
		// No se graba una referencia mala: la espera tuvo que ver un cambio y
		// terminar estable, y el recorte no puede ser fondo plano.
		if !out.Changed || !out.Stable {
			t.Fatalf("no se graba la referencia: la espera tras 'Facturas' termino con Changed=%v Stable=%v", out.Changed, out.Stable)
		}
		if v := lumaVariance(header); v < minHeaderVariance {
			t.Fatalf("no se graba la referencia: el recorte %v tiene varianza %.1f (< %.0f), parece fondo plano", header.Rect, v, minHeaderVariance)
		}
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
		// Fuera de t.TempDir para que sobreviva al test y se pueda inspeccionar.
		dir, err := os.MkdirTemp("", "e2_6_s5_fallo_")
		if err != nil {
			t.Fatal(err)
		}
		got := filepath.Join(dir, "facturas_header_obtenido.png")
		writePNG(t, got, header)
		writePNG(t, filepath.Join(dir, "pantalla_completa.png"), out.Screen)
		t.Fatalf("el encabezado difiere de la referencia en %.1f%% de los pixeles (Changed=%v Stable=%v Settle=%v; capturas en %s)", d*100, out.Changed, out.Stable, out.Settle, dir)
	}
}

// Coordenadas por defecto del escritorio de SIGECOM (1280x800), relevadas por
// el validador de la iteracion 1 y revisadas sobre v4.2 (epica 10: menus y
// encabezado en el mismo lugar, modal nuevo al abrir; la referencia del
// encabezado se regrabo porque cambiaron los colores del tema). No son
// secretos; las variables de entorno SO_E2_6_* las reemplazan si el
// escritorio cambia.
const (
	defDismissXY  = "840,483"
	defCarteraXY  = "204,48"
	defFacturasXY = "208,76"
	defHeaderRect = "14,184,1250,22"
	// Un encabezado con texto tiene varianza de luminancia muy por encima de
	// esto; un fondo plano (o casi, con un pixel de cursor) queda por debajo.
	minHeaderVariance = 100.0
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// lumaVariance es la varianza de la luminancia (0..255) de img.
func lumaVariance(img *image.RGBA) float64 {
	var sum, sum2, n float64
	b := img.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			l := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
			sum += l
			sum2 += l * l
			n++
		}
	}
	if n == 0 {
		return 0
	}
	m := sum / n
	return sum2/n - m*m
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
