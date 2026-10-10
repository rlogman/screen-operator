package screen

import (
	"context"
	"image"
	"testing"
	"time"
)

// staticDriver es un Driver en memoria con la pantalla quieta.
type staticDriver struct{ img *image.RGBA }

func (d staticDriver) Size() (int, int) { return d.img.Rect.Dx(), d.img.Rect.Dy() }
func (d staticDriver) Name() string     { return "static" }
func (d staticDriver) Capture(context.Context) (*image.RGBA, error) {
	out := image.NewRGBA(d.img.Rect)
	copy(out.Pix, d.img.Pix)
	return out, nil
}
func (staticDriver) Pointer(int, int, uint8) error { return nil }
func (staticDriver) Key(uint32, bool) error        { return nil }
func (staticDriver) Close() error                  { return nil }

// Arrastre del validador de la epica 2: el horizonte de cambio se puede acortar
// por tipo de accion (surface.settle.horizon_by_action) sin cambiar el resto.
func TestSettleHorizonByAction(t *testing.T) {
	surf, _ := kiosk(t)
	if got := surf.Settle.Horizon("type"); got != 800 {
		t.Fatalf("vnc-kiosk: horizonte de type = %d, se esperaba 800", got)
	}
	if got := surf.Settle.Horizon("click"); got != surf.Settle.ChangeHorizonMS {
		t.Fatalf("click debe usar change_horizon_ms (%d), dio %d", surf.Settle.ChangeHorizonMS, got)
	}
	d := staticDriver{img: image.NewRGBA(image.Rect(0, 0, 64, 40))}
	before, _ := d.Capture(context.Background())
	s := NewSettler(d, surf.Settle)

	res, err := s.WaitAction(context.Background(), before, "type")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stable || res.Elapsed < 800*time.Millisecond || res.Elapsed > 1300*time.Millisecond {
		t.Fatalf("type: espera %v estable=%v, se esperaba ~800 ms", res.Elapsed, res.Stable)
	}
	res, err = s.WaitAction(context.Background(), before, "click")
	if err != nil {
		t.Fatal(err)
	}
	if res.Elapsed < time.Duration(surf.Settle.ChangeHorizonMS)*time.Millisecond {
		t.Fatalf("click: espera %v, menor que change_horizon_ms", res.Elapsed)
	}
}
