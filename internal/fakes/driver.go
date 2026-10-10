package fakes

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"sync"
)

// DriverEvent es un evento de entrada recibido por el Driver falso.
type DriverEvent struct {
	Pointer bool
	X, Y    int
	Buttons uint8
	Keysym  uint32
	Down    bool
}

// Driver es un screen.Driver en memoria: una pantalla que cambia solo cuando
// un test lo pide (SetScreen) o cuando OnClick reacciona a un clic. Registra
// todos los eventos. No abre sockets (el sandbox no deja bindear).
type Driver struct {
	mu      sync.Mutex
	img     *image.RGBA
	events  []DriverEvent
	buttons uint8
	closed  bool
	// OnClick se llama con el punto de cada clic izquierdo (al soltar). Puede
	// llamar a SetScreen.
	OnClick func(d *Driver, x, y int)
	// BlockInput hace que Pointer y Key bloqueen hasta que se cierre el canal.
	BlockInput chan struct{}
	captures   int
}

// NewDriver crea una pantalla w x h de un color.
func NewDriver(w, h int, bg color.RGBA) *Driver {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, &image.Uniform{bg}, image.Point{}, draw.Src)
	return &Driver{img: img}
}

// NewDriverFrom crea el driver con una imagen inicial.
func NewDriverFrom(img image.Image) *Driver {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	return &Driver{img: out}
}

// SetScreen reemplaza la pantalla.
func (d *Driver) SetScreen(img image.Image) {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	d.mu.Lock()
	d.img = out
	d.mu.Unlock()
}

// Fill pinta un rectangulo (para provocar cambios visibles).
func (d *Driver) Fill(r image.Rectangle, c color.RGBA) {
	d.mu.Lock()
	draw.Draw(d.img, r, &image.Uniform{c}, image.Point{}, draw.Src)
	d.mu.Unlock()
}

// Size implementa screen.Driver.
func (d *Driver) Size() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.img.Rect.Dx(), d.img.Rect.Dy()
}

// Name implementa screen.Driver.
func (d *Driver) Name() string { return "fake" }

// Capture implementa screen.Driver.
func (d *Driver) Capture(ctx context.Context) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("fakes: driver cerrado")
	}
	d.captures++
	out := image.NewRGBA(d.img.Rect)
	copy(out.Pix, d.img.Pix)
	return out, nil
}

// Pointer implementa screen.Driver.
func (d *Driver) Pointer(x, y int, buttons uint8) error {
	if d.BlockInput != nil {
		<-d.BlockInput
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("fakes: driver cerrado")
	}
	prev := d.buttons
	d.buttons = buttons
	d.events = append(d.events, DriverEvent{Pointer: true, X: x, Y: y, Buttons: buttons})
	cb := d.OnClick
	d.mu.Unlock()
	if prev&1 != 0 && buttons&1 == 0 && cb != nil {
		cb(d, x, y)
	}
	return nil
}

// Key implementa screen.Driver.
func (d *Driver) Key(keysym uint32, down bool) error {
	if d.BlockInput != nil {
		<-d.BlockInput
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("fakes: driver cerrado")
	}
	d.events = append(d.events, DriverEvent{Keysym: keysym, Down: down})
	return nil
}

// Close implementa screen.Driver.
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// Events devuelve una copia de los eventos recibidos.
func (d *Driver) Events() []DriverEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DriverEvent(nil), d.events...)
}

// Closed indica si se cerro.
func (d *Driver) Closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}
