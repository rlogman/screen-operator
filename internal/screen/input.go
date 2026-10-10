package screen

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Input traduce gestos (clic, doble clic, escribir, combinaciones, rueda) a
// eventos del Driver con la temporizacion de surface.input. No espera
// estabilidad: eso lo hace Executor.
type Input struct {
	d   Driver
	cfg InputConfig
}

// NewInput crea el traductor de gestos.
func NewInput(d Driver, cfg InputConfig) *Input { return &Input{d: d, cfg: cfg} }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (in *Input) inside(x, y int) error {
	w, h := in.d.Size()
	if x < 0 || y < 0 || x >= w || y >= h {
		return fmt.Errorf("screen: punto (%d,%d) fuera de la pantalla %dx%d", x, y, w, h)
	}
	return nil
}

// Move mueve el puntero sin botones.
func (in *Input) Move(x, y int) error {
	if err := in.inside(x, y); err != nil {
		return err
	}
	return in.d.Pointer(x, y, 0)
}

// press hace down, espera click_hold_ms y up. El puntero ya debe estar en (x,y).
func (in *Input) press(ctx context.Context, x, y int, button uint8) error {
	if err := in.d.Pointer(x, y, button); err != nil {
		return err
	}
	holdErr := sleepCtx(ctx, ms(in.cfg.ClickHoldMS))
	// El up se envia aunque el contexto se cancele: un boton colgado rompe la sesion.
	if err := in.d.Pointer(x, y, 0); err != nil {
		return err
	}
	return holdErr
}

// Click mueve a (x,y) y hace clic con el boton dado (por defecto el izquierdo)
// manteniendo click_hold_ms entre down y up.
func (in *Input) Click(ctx context.Context, x, y int, button uint8) error {
	if button == 0 {
		button = ButtonLeft
	}
	if err := in.Move(x, y); err != nil {
		return err
	}
	return in.press(ctx, x, y, button)
}

// MultiClick hace n clics izquierdos separados por double_click_gap_ms.
func (in *Input) MultiClick(ctx context.Context, x, y, n int) error {
	if err := in.Move(x, y); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, ms(in.cfg.DoubleClickGapMS)); err != nil {
				return err
			}
		}
		if err := in.press(ctx, x, y, ButtonLeft); err != nil {
			return err
		}
	}
	return nil
}

// DoubleClick son dos clics separados por double_click_gap_ms.
func (in *Input) DoubleClick(ctx context.Context, x, y int) error { return in.MultiClick(ctx, x, y, 2) }

// TripleClick son tres clics separados por double_click_gap_ms.
func (in *Input) TripleClick(ctx context.Context, x, y int) error { return in.MultiClick(ctx, x, y, 3) }

// Wheel envia n pulsos de rueda en (x,y). dir: up, down, left o right.
// Cada pulso es un down/up del boton de rueda (4/5/6/7) con key_hold_ms entre ambos.
func (in *Input) Wheel(ctx context.Context, x, y int, dir string, n int) error {
	var b uint8
	switch strings.ToLower(dir) {
	case "up", "arriba":
		b = WheelUp
	case "down", "abajo":
		b = WheelDown
	case "left", "izquierda":
		b = WheelLeft
	case "right", "derecha":
		b = WheelRight
	default:
		return fmt.Errorf("screen: direccion de scroll %q (se espera up, down, left o right)", dir)
	}
	if n < 1 {
		return fmt.Errorf("screen: scroll de %d pasos", n)
	}
	if err := in.Move(x, y); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if err := in.d.Pointer(x, y, b); err != nil {
			return err
		}
		if err := sleepCtx(ctx, ms(in.cfg.KeyHoldMS)); err != nil {
			in.d.Pointer(x, y, 0)
			return err
		}
		if err := in.d.Pointer(x, y, 0); err != nil {
			return err
		}
		if err := sleepCtx(ctx, ms(in.cfg.KeyHoldMS)); err != nil {
			return err
		}
	}
	return nil
}

// Keys presiona los keysyms en orden, espera key_hold_ms y los suelta en orden
// inverso (ctrl+a: Control down, a down, a up, Control up).
func (in *Input) Keys(ctx context.Context, keys ...uint32) error {
	for i, k := range keys {
		if err := in.d.Key(k, true); err != nil {
			in.release(keys[:i])
			return err
		}
	}
	holdErr := sleepCtx(ctx, ms(in.cfg.KeyHoldMS))
	if err := in.release(keys); err != nil {
		return err
	}
	return holdErr
}

func (in *Input) release(keys []uint32) error {
	var first error
	for i := len(keys) - 1; i >= 0; i-- {
		if err := in.d.Key(keys[i], false); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Combo envia una combinacion por nombre, p. ej. "ctrl+a" o "enter".
func (in *Input) Combo(ctx context.Context, combo string) error {
	keys, err := ParseCombo(combo)
	if err != nil {
		return err
	}
	return in.Keys(ctx, keys...)
}

// Type escribe el texto caracter a caracter (down/up por tecla) con
// type_char_delay_ms entre caracteres. Valida todo el texto antes de enviar.
func (in *Input) Type(ctx context.Context, text string) error {
	keys := make([]uint32, 0, len(text))
	for _, r := range text {
		k, err := KeysymForRune(r)
		if err != nil {
			return err
		}
		keys = append(keys, k)
	}
	for i, k := range keys {
		if i > 0 {
			if err := sleepCtx(ctx, ms(in.cfg.TypeCharDelayMS)); err != nil {
				return err
			}
		}
		if err := in.Keys(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
