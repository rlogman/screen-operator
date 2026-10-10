package screen

import (
	"context"
	"image"
	"time"
)

// Settled es el resultado de esperar pantalla estable.
type Settled struct {
	Image   *image.RGBA   // ultima captura
	Stable  bool          // stable_frames lecturas seguidas sin cambio al terminar
	Changed bool          // hubo cambio respecto a la captura previa a la accion
	Elapsed time.Duration // duracion de la espera
	Frames  int           // capturas tomadas
}

// Settler espera a que el framebuffer deje de cambiar (surface.settle).
type Settler struct {
	d   Driver
	cfg SettleConfig
}

// NewSettler crea el detector de pantalla estable.
func NewSettler(d Driver, cfg SettleConfig) *Settler { return &Settler{d: d, cfg: cfg} }

// DiffFraction es la fraccion de pixeles distintos entre a y b (1 si los
// tamanos no coinciden). El canal alfa se ignora.
func DiffFraction(a, b *image.RGBA) float64 {
	if a.Rect != b.Rect {
		return 1
	}
	total := a.Rect.Dx() * a.Rect.Dy()
	if total == 0 {
		return 0
	}
	diff := 0
	for y := 0; y < a.Rect.Dy(); y++ {
		pa := a.Pix[y*a.Stride : y*a.Stride+a.Rect.Dx()*4]
		pb := b.Pix[y*b.Stride : y*b.Stride+b.Rect.Dx()*4]
		for i := 0; i < len(pa); i += 4 {
			if pa[i] != pb[i] || pa[i+1] != pb[i+1] || pa[i+2] != pb[i+2] {
				diff++
			}
		}
	}
	return float64(diff) / float64(total)
}

// Same indica si dos capturas son iguales salvo una fraccion <= threshold
// (p. ej. el cursor o un parpadeo de un pixel).
func Same(a, b *image.RGBA, threshold float64) bool {
	return DiffFraction(a, b) <= threshold
}

// Wait captura cada poll_ms hasta que se cumplan a la vez:
//   - paso min_wait_ms desde el inicio;
//   - las ultimas stable_frames comparaciones entre capturas consecutivas no
//     superan diff_threshold;
//   - si before no es nil (captura previa a una accion), paso change_horizon_ms
//     desde el inicio, haya habido cambio o no. El escritorio da feedback
//     inmediato (resaltado, hover) y repinta de verdad hasta 1 s despues: un
//     cambio temprano no prueba que el repintado ya llego. Un cambio que llega
//     tarde pone a cero la racha de comparaciones quietas, asi que la ventana
//     de quietud se reinicia y la captura devuelta es la del ultimo repintado.
//
// Termina a mas tardar en max_wait_ms; entonces Stable indica si la pantalla
// estaba quieta en ese momento. Changed informa si hubo cambio respecto a
// before, pero ya no adelanta la salida.
func (s *Settler) Wait(ctx context.Context, before *image.RGBA) (Settled, error) {
	start := time.Now()
	minWait, maxWait, poll := ms(s.cfg.MinWaitMS), ms(s.cfg.MaxWaitMS), ms(s.cfg.PollMS)
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	if maxWait < minWait {
		maxWait = minWait
	}
	need := s.cfg.StableFrames
	if need < 1 {
		need = 1
	}
	deadline := start.Add(maxWait)
	horizon := start.Add(ms(s.cfg.ChangeHorizonMS))
	afterAction := before != nil && s.cfg.ChangeHorizonMS > 0

	prev, err := s.d.Capture(ctx)
	if err != nil {
		return Settled{}, err
	}
	res := Settled{Image: prev, Frames: 1}
	res.Changed = before != nil && !Same(before, prev, s.cfg.DiffThreshold)
	run := 0
	for {
		now := time.Now()
		quiet := run >= need
		if now.Sub(start) >= minWait && quiet && (!afterAction || !now.Before(horizon)) {
			res.Stable = true
			break
		}
		if !now.Before(deadline) {
			res.Stable = quiet
			break
		}
		if err := sleepCtx(ctx, min(poll, deadline.Sub(now))); err != nil {
			return res, err
		}
		cur, err := s.d.Capture(ctx)
		if err != nil {
			return res, err
		}
		res.Frames++
		if Same(prev, cur, s.cfg.DiffThreshold) {
			run++
		} else {
			run = 0
		}
		if before != nil && !res.Changed && !Same(before, cur, s.cfg.DiffThreshold) {
			res.Changed = true
		}
		prev = cur
		res.Image = cur
	}
	res.Elapsed = time.Since(start)
	return res, nil
}
