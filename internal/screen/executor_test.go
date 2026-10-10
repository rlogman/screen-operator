package screen

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"

	"screenoperator/internal/fakes/rfbserver"
)

var (
	fieldRect = image.Rect(400, 280, 700, 320)
	surfaceBG = color.RGBA{240, 240, 240, 255} // fondo por defecto del servidor falso
)

func TestE2_6_S1_TypeReplaceSeleccionaTodoAntesDeEscribir(t *testing.T) {
	surf, perc := kiosk(t)
	if surf.Input.ReplaceText.Strategy != "select_all_then_type" {
		t.Fatalf("vnc-kiosk usa %q", surf.Input.ReplaceText.Strategy)
	}
	srv, d := fakeDesktop(t, rfbserver.Options{MinClickHold: 100 * time.Millisecond})
	srv.AddTextField(fieldRect, "viejo")
	if _, err := NewExecutor(d, surf, perc).TypeReplace(context.Background(), 500, 300, "nuevo"); err != nil {
		t.Fatal(err)
	}
	if got := srv.FieldText(); got != "nuevo" {
		t.Fatalf("el campo quedo con %q, se esperaba 'nuevo'", got)
	}
	ev := keyEvents(srv.Events())
	ctrl, a, firstN := -1, -1, -1
	for i, e := range ev {
		switch {
		case e.Keysym == 0xffe3 && e.Down && ctrl < 0:
			ctrl = i
		case e.Keysym == 'a' && e.Down && ctrl >= 0 && a < 0:
			a = i
		case e.Keysym == 'n' && e.Down && firstN < 0:
			firstN = i
		}
	}
	if ctrl < 0 || a < 0 || firstN < 0 || !(ctrl < a && a < firstN) {
		t.Fatalf("orden de teclas ctrl=%d a=%d primera 'n'=%d; se esperaba ctrl+a antes de 'nuevo'", ctrl, a, firstN)
	}
}

func TestE2_6_S2_TypeReplaceConTripleClic(t *testing.T) {
	surf, perc := kiosk(t)
	surf.Input.ReplaceText.Strategy = "triple_click_then_type"
	srv, d := fakeDesktop(t, rfbserver.Options{MinClickHold: 100 * time.Millisecond})
	srv.AddTextField(fieldRect, "viejo")
	if _, err := NewExecutor(d, surf, perc).TypeReplace(context.Background(), 500, 300, "nuevo"); err != nil {
		t.Fatal(err)
	}
	cl := srv.Clicks()
	if len(cl) != 3 {
		t.Fatalf("%d clics, se esperaba un triple clic", len(cl))
	}
	for _, c := range cl {
		if c.Button != 1 || c.X != 500 || c.Y != 300 || c.Ignored {
			t.Fatalf("clic %+v", c)
		}
	}
	for _, e := range keyEvents(srv.Events()) {
		if e.Keysym == 0xffe3 || e.Keysym == 0xffe4 {
			t.Fatal("se envio Control con la estrategia triple_click_then_type")
		}
	}
	if got := srv.FieldText(); got != "nuevo" {
		t.Fatalf("el campo quedo con %q", got)
	}
}

func TestE2_6_S3_HoverSinBotonesRespetaDwell(t *testing.T) {
	surf, perc := kiosk(t)
	if surf.Input.HoverDwellMS != 600 {
		t.Fatalf("vnc-kiosk hover_dwell_ms=%d, el escenario asume 600", surf.Input.HoverDwellMS)
	}
	srv, d := fakeDesktop(t, rfbserver.Options{})
	start := time.Now()
	out, err := NewExecutor(d, surf, perc).Hover(context.Background(), 270, 182)
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 600*time.Millisecond || out.Duration < 600*time.Millisecond {
		t.Fatalf("Hover retorno a los %v (Duration %v), antes de 600ms", el, out.Duration)
	}
	ev := srv.Events()
	if len(ev) == 0 {
		t.Fatal("no se registro movimiento")
	}
	for _, e := range ev {
		if e.Kind != "pointer" || e.Buttons != 0 || e.X != 270 || e.Y != 182 {
			t.Fatalf("evento %+v; se esperaba solo movimiento sin botones a (270,182)", e)
		}
	}
}

func TestE2_6_S4_PrimitivasDevuelvenCapturaPosteriorYDuracion(t *testing.T) {
	surf, perc := kiosk(t)
	srv, d := fakeDesktop(t, rfbserver.Options{RepaintDelay: 800 * time.Millisecond, MinClickHold: 100 * time.Millisecond})
	srv.AddTextField(fieldRect, "")
	// Cada clic aceptado fuera del campo repinta un bloque con retraso de 800 ms.
	srv.OnClick(func(s *rfbserver.Server, c rfbserver.Click) {
		if image.Pt(c.X, c.Y).In(fieldRect) {
			return
		}
		s.FillRect(image.Rect(0, 400, 1280, 800), green)
	})
	ex := NewExecutor(d, surf, perc)
	ctx := context.Background()
	steps := []struct {
		name string
		run  func() (Outcome, error)
	}{
		{"Click", func() (Outcome, error) { return ex.Click(ctx, 100, 500) }},
		{"DoubleClick", func() (Outcome, error) { return ex.DoubleClick(ctx, 100, 500) }},
		{"Hover", func() (Outcome, error) { return ex.Hover(ctx, 200, 200) }},
		{"TypeReplace", func() (Outcome, error) { return ex.TypeReplace(ctx, 500, 300, "abc") }},
		{"Key", func() (Outcome, error) { return ex.Key(ctx, "enter") }},
		{"Scroll", func() (Outcome, error) { return ex.Scroll(ctx, 640, 400, "down", 1) }},
	}
	for _, st := range steps {
		start := time.Now()
		out, err := st.run()
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if out.Screen == nil || out.Screen.Bounds().Dx() != 1280 || out.Screen.Bounds().Dy() != 800 {
			t.Fatalf("%s: sin captura de pantalla completa", st.name)
		}
		if out.Duration <= 0 || out.Duration < out.Settle || out.Duration > time.Since(start) {
			t.Fatalf("%s: duracion %v / espera %v incoherentes", st.name, out.Duration, out.Settle)
		}
		if out.Settle < ms(surf.Settle.MinWaitMS) {
			t.Fatalf("%s: espera de estabilidad de %v < min_wait_ms", st.name, out.Settle)
		}
		if st.name == "Click" {
			// La captura es posterior al repintado retrasado.
			if got := out.Screen.RGBAAt(640, 600); got != green || !out.Changed {
				t.Fatalf("Click: la captura devuelta no tiene el repintado (pixel %v, %+v)", got, out)
			}
			srv.FillRect(image.Rect(0, 400, 1280, 800), surfaceBG)
		}
	}
	if srv.FieldText() != "abc" {
		t.Fatalf("campo %q", srv.FieldText())
	}
}
