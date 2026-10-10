package screen

import (
	"context"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"

	"screenoperator/internal/fakes/rfbserver"
)

var green = color.RGBA{0, 180, 0, 255}

func TestE2_5_S1_EsperaElRepintadoRetrasado(t *testing.T) {
	surf, _ := kiosk(t) // valores reales de vnc-kiosk
	srv, d := fakeDesktop(t, rfbserver.Options{RepaintDelay: 800 * time.Millisecond, MinClickHold: 100 * time.Millisecond})
	srv.OnClick(func(s *rfbserver.Server, c rfbserver.Click) {
		s.FillRect(image.Rect(300, 200, 900, 600), green)
	})
	ctx := context.Background()
	before, err := d.Capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewInput(d, surf.Input).Click(ctx, 500, 300, 0); err != nil {
		t.Fatal(err)
	}
	res, err := NewSettler(d, surf.Settle).Wait(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Image.RGBAAt(600, 400); got != green {
		t.Fatalf("la captura tras la espera tiene %v en (600,400); se esperaba el contenido nuevo (verde). %+v", got, res)
	}
	if !res.Stable || !res.Changed {
		t.Fatalf("resultado %+v, se esperaba estable y con cambio", res)
	}
}

func TestE2_5_S2_PantallaQuietaRespetaMinWait(t *testing.T) {
	surf, _ := kiosk(t)
	cfg := surf.Settle
	cfg.MinWaitMS = 400
	_, d := fakeDesktop(t, rfbserver.Options{})
	res, err := NewSettler(d, cfg).Wait(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	maxAllowed := 400*time.Millisecond + 2*ms(cfg.PollMS)
	if res.Elapsed < 400*time.Millisecond || res.Elapsed > maxAllowed {
		t.Fatalf("espera de %v, se esperaba entre 400ms y %v", res.Elapsed, maxAllowed)
	}
	if !res.Stable {
		t.Fatalf("pantalla quieta reportada como no estable: %+v", res)
	}
}

func TestE2_5_S3_PantallaQueCambiaAgotaMaxWait(t *testing.T) {
	surf, _ := kiosk(t)
	cfg := surf.Settle
	cfg.MaxWaitMS = 2500
	srv, d := fakeDesktop(t, rfbserver.Options{})
	srv.SetAnimate(true)
	res, err := NewSettler(d, cfg).Wait(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Elapsed < 2500*time.Millisecond || res.Elapsed > 2700*time.Millisecond {
		t.Fatalf("espera de %v, se esperaba entre 2500 y 2700 ms", res.Elapsed)
	}
	if res.Stable {
		t.Fatalf("pantalla en movimiento reportada como estable: %+v", res)
	}
}

// blinkDriver es un Driver en memoria que cambia un solo pixel (como el cursor)
// en cada captura.
type blinkDriver struct {
	mu  sync.Mutex
	img *image.RGBA
	n   int
}

func (b *blinkDriver) Size() (int, int) { return 1280, 800 }
func (b *blinkDriver) Name() string     { return "blink" }
func (b *blinkDriver) Capture(context.Context) (*image.RGBA, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n++
	b.img.SetRGBA(640, 400, color.RGBA{uint8(b.n * 50), 0, 0, 255})
	out := image.NewRGBA(b.img.Rect)
	copy(out.Pix, b.img.Pix)
	return out, nil
}
func (b *blinkDriver) Pointer(int, int, uint8) error { return nil }
func (b *blinkDriver) Key(uint32, bool) error        { return nil }
func (b *blinkDriver) Close() error                  { return nil }

func TestE2_5_S4_UnPixelBajoElUmbralEsEstable(t *testing.T) {
	surf, _ := kiosk(t)
	cfg := surf.Settle
	if cfg.DiffThreshold != 0.002 {
		t.Fatalf("vnc-kiosk diff_threshold=%v, el escenario asume 0.002", cfg.DiffThreshold)
	}
	a := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	b := image.NewRGBA(a.Rect)
	b.SetRGBA(10, 10, color.RGBA{255, 255, 255, 255})
	if !Same(a, b, cfg.DiffThreshold) {
		t.Fatalf("un pixel distinto (fraccion %v) no se considero estable", DiffFraction(a, b))
	}
	d := &blinkDriver{img: image.NewRGBA(a.Rect)}
	res, err := NewSettler(d, cfg).Wait(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stable || res.Elapsed > ms(cfg.MaxWaitMS) {
		t.Fatalf("un cursor que parpadea impidio la estabilidad: %+v", res)
	}
}

// Trampa del escritorio real (iteracion 2): el clic produce de inmediato un
// cambio pequeno pero mayor que diff_threshold (el resaltado de un item de
// menu, 6.000 px) y el repintado real llega 800 ms despues. La espera no puede
// darse por estable con el resaltado: tiene que devolver el repintado.
func TestE2_5_S1_ImmediateFeedbackThenRepaint(t *testing.T) {
	surf, _ := kiosk(t) // valores reales de vnc-kiosk
	highlight := color.RGBA{60, 90, 200, 255}
	srv, d := fakeDesktop(t, rfbserver.Options{RepaintDelay: 800 * time.Millisecond, MinClickHold: 100 * time.Millisecond})
	srv.OnClickFeedback(func(s *rfbserver.Server, c rfbserver.Click) {
		s.FillRect(image.Rect(200, 70, 400, 100), highlight)
	})
	srv.OnClick(func(s *rfbserver.Server, c rfbserver.Click) {
		s.FillRect(image.Rect(300, 200, 900, 600), green)
	})
	ctx := context.Background()
	before, err := d.Capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewInput(d, surf.Input).Click(ctx, 500, 300, 0); err != nil {
		t.Fatal(err)
	}
	res, err := NewSettler(d, surf.Settle).Wait(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Image.RGBAAt(250, 80); got != highlight {
		t.Fatalf("el feedback inmediato no aparece en la captura final (%v): el falso no emula la trampa", got)
	}
	if got := res.Image.RGBAAt(600, 400); got != green {
		t.Fatalf("la espera termino con el feedback inmediato y sin el repintado: (600,400)=%v, %+v", got, res)
	}
	if !res.Stable || !res.Changed {
		t.Fatalf("resultado %+v, se esperaba estable y con cambio", res)
	}
	if res.Elapsed > ms(surf.Settle.MaxWaitMS)+ms(surf.Settle.PollMS) {
		t.Fatalf("la espera de %v supera max_wait_ms", res.Elapsed)
	}
}

// Un cambio tardio (despues de que ya hubo quietud) reinicia la ventana de
// quietud: la espera devuelve el ultimo repintado y no corta antes de
// max_wait_ms aunque el repintado llegue cerca del horizonte.
func TestSettleLateChangeRestartsQuietWindow(t *testing.T) {
	surf, _ := kiosk(t)
	srv, d := fakeDesktop(t, rfbserver.Options{RepaintDelay: 950 * time.Millisecond, MinClickHold: 100 * time.Millisecond})
	srv.OnClickFeedback(func(s *rfbserver.Server, c rfbserver.Click) {
		s.FillRect(image.Rect(200, 70, 400, 100), color.RGBA{60, 90, 200, 255})
	})
	srv.OnClick(func(s *rfbserver.Server, c rfbserver.Click) {
		s.FillRect(image.Rect(300, 200, 900, 600), green)
	})
	ctx := context.Background()
	before, err := d.Capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewInput(d, surf.Input).Click(ctx, 500, 300, 0); err != nil {
		t.Fatal(err)
	}
	res, err := NewSettler(d, surf.Settle).Wait(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Image.RGBAAt(600, 400); got != green || !res.Stable {
		t.Fatalf("repintado a 950 ms no devuelto: (600,400)=%v, %+v", got, res)
	}
}
