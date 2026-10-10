package screen

import (
	"context"
	"fmt"
	"image"
	"time"
)

// backspaceN es cuantas veces borra la estrategia backspace_n_then_type. El
// esquema no fija N; 64 cubre los campos de formulario habituales.
const backspaceN = 64

// Outcome es el resultado de una primitiva: la captura posterior a la espera
// de estabilidad y cuanto tardo todo.
type Outcome struct {
	Screen   *image.RGBA   // captura a resolucion de pantalla tras la espera
	Stable   bool          // la pantalla quedo quieta
	Changed  bool          // hubo cambio visible respecto a antes de la accion
	Settle   time.Duration // parte de Duration dedicada a esperar estabilidad
	Duration time.Duration // duracion total de la primitiva
}

// Executor es la API de alto nivel que usa el agente. Todas las coordenadas
// son de pantalla; para convertir desde la imagen del modelo usar
// ModelShot.ToScreen.
type Executor struct {
	d   Driver
	s   Surface
	p   Perception
	in  *Input
	set *Settler
}

// NewExecutor arma el executor sobre un Driver ya conectado.
func NewExecutor(d Driver, s Surface, p Perception) *Executor {
	return &Executor{d: d, s: s, p: p, in: NewInput(d, s.Input), set: NewSettler(d, s.Settle)}
}

// Driver devuelve el transporte subyacente.
func (e *Executor) Driver() Driver { return e.d }

// act captura antes, ejecuta la accion y espera estabilidad.
func (e *Executor) act(ctx context.Context, action func() error) (Outcome, error) {
	start := time.Now()
	before, err := e.d.Capture(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if err := action(); err != nil {
		return Outcome{Duration: time.Since(start)}, err
	}
	res, err := e.set.Wait(ctx, before)
	out := Outcome{Screen: res.Image, Stable: res.Stable, Changed: res.Changed, Settle: res.Elapsed, Duration: time.Since(start)}
	return out, err
}

// Screenshot espera pantalla estable (sin accion previa) y devuelve la captura.
func (e *Executor) Screenshot(ctx context.Context) (Outcome, error) {
	start := time.Now()
	res, err := e.set.Wait(ctx, nil)
	return Outcome{Screen: res.Image, Stable: res.Stable, Settle: res.Elapsed, Duration: time.Since(start)}, err
}

// ForModel reduce una captura a perception.screenshot.max_width.
func (e *Executor) ForModel(img *image.RGBA) ModelShot {
	return ForModel(img, e.p.Screenshot.MaxWidth)
}

// EncodeForModel reduce y codifica segun perception.screenshot.
func (e *Executor) EncodeForModel(img *image.RGBA) (ModelShot, []byte, string, error) {
	shot := e.ForModel(img)
	b, mime, err := e.p.Encode(shot.Image)
	return shot, b, mime, err
}

// Click hace clic izquierdo en (x,y).
func (e *Executor) Click(ctx context.Context, x, y int) (Outcome, error) {
	return e.act(ctx, func() error { return e.in.Click(ctx, x, y, ButtonLeft) })
}

// RightClick hace clic derecho en (x,y).
func (e *Executor) RightClick(ctx context.Context, x, y int) (Outcome, error) {
	return e.act(ctx, func() error { return e.in.Click(ctx, x, y, ButtonRight) })
}

// DoubleClick hace doble clic en (x,y).
func (e *Executor) DoubleClick(ctx context.Context, x, y int) (Outcome, error) {
	return e.act(ctx, func() error { return e.in.DoubleClick(ctx, x, y) })
}

// Hover mueve el puntero sin botones y espera hover_dwell_ms antes de buscar
// estabilidad (submenus que abren con hover).
func (e *Executor) Hover(ctx context.Context, x, y int) (Outcome, error) {
	return e.act(ctx, func() error {
		if err := e.in.Move(x, y); err != nil {
			return err
		}
		return sleepCtx(ctx, ms(e.s.Input.HoverDwellMS))
	})
}

// TypeReplace reemplaza el contenido del campo en (x,y) por text segun
// surface.input.replace_text.strategy. Escribir sobre un campo con texto
// concatena, por eso nunca se escribe sin seleccionar o borrar antes.
func (e *Executor) TypeReplace(ctx context.Context, x, y int, text string) (Outcome, error) {
	return e.act(ctx, func() error {
		if err := e.clearField(ctx, x, y); err != nil {
			return err
		}
		if text == "" {
			return e.in.Keys(ctx, KeyDelete)
		}
		return e.in.Type(ctx, text)
	})
}

func (e *Executor) clearField(ctx context.Context, x, y int) error {
	rt := e.s.Input.ReplaceText
	switch rt.Strategy {
	case "select_all_then_type", "":
		if err := e.in.Click(ctx, x, y, ButtonLeft); err != nil {
			return err
		}
		chord := rt.Chord
		if len(chord) == 0 {
			chord = []string{"ctrl", "a"}
		}
		keys := make([]uint32, 0, len(chord))
		for _, name := range chord {
			k, err := keyByName(name)
			if err != nil {
				return fmt.Errorf("screen: surface.input.replace_text.chord: %w", err)
			}
			keys = append(keys, k)
		}
		return e.in.Keys(ctx, keys...)
	case "triple_click_then_type":
		return e.in.TripleClick(ctx, x, y)
	case "backspace_n_then_type":
		if err := e.in.Click(ctx, x, y, ButtonLeft); err != nil {
			return err
		}
		if err := e.in.Keys(ctx, KeyEnd); err != nil {
			return err
		}
		for i := 0; i < backspaceN; i++ {
			if err := e.in.Keys(ctx, KeyBackSpace); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("screen: estrategia replace_text %q no implementada", rt.Strategy)
}

// Type escribe en el foco actual sin borrar antes (p. ej. un buscador vacio).
func (e *Executor) Type(ctx context.Context, text string) (Outcome, error) {
	return e.act(ctx, func() error { return e.in.Type(ctx, text) })
}

// Key envia una tecla o combinacion ("enter", "ctrl+a").
func (e *Executor) Key(ctx context.Context, combo string) (Outcome, error) {
	keys, err := ParseCombo(combo)
	if err != nil {
		return Outcome{}, err
	}
	return e.act(ctx, func() error { return e.in.Keys(ctx, keys...) })
}

// Scroll desplaza steps pasos en dir; cada paso son
// surface.input.scroll.wheel_clicks_per_step pulsos de rueda.
func (e *Executor) Scroll(ctx context.Context, x, y int, dir string, steps int) (Outcome, error) {
	per := e.s.Input.Scroll.WheelClicksPerStep
	if per < 1 {
		per = 1
	}
	return e.act(ctx, func() error { return e.in.Wheel(ctx, x, y, dir, steps*per) })
}

// Zoom captura la pantalla actual y amplia la region r (coordenadas de
// pantalla) por perception.zoom.factor. No modifica la pantalla.
func (e *Executor) Zoom(ctx context.Context, r image.Rectangle) (*image.RGBA, error) {
	if !e.p.Zoom.Enabled {
		return nil, fmt.Errorf("screen: zoom deshabilitado (perception.zoom.enabled=false)")
	}
	f := e.p.Zoom.Factor
	if f < 1 {
		f = 2
	}
	img, err := e.d.Capture(ctx)
	if err != nil {
		return nil, err
	}
	return Zoom(img, r, f)
}
