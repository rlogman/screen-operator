package screen

import (
	"context"
	"fmt"
	"image"
	"time"

	"screenoperator/internal/rfb"
)

// Driver es el transporte hacia el escritorio. El resto del agente solo habla
// con esta interfaz: otro protocolo (RDP, X11) se agrega con otro Driver y un
// valor nuevo en surface.transport, sin tocar primitivas ni agente.
//
// Las coordenadas son de pantalla (pixeles del framebuffer). Las teclas son
// keysyms X11, un vocabulario neutro que otro transporte puede traducir.
type Driver interface {
	// Size es el tamano del framebuffer.
	Size() (width, height int)
	// Name es el nombre del escritorio que anuncia el servidor.
	Name() string
	// Capture devuelve una copia del framebuffer actualizada.
	Capture(ctx context.Context) (*image.RGBA, error)
	// Pointer mueve el puntero a (x,y) con la mascara de botones dada.
	Pointer(x, y int, buttons uint8) error
	// Key presiona (down) o suelta una tecla.
	Key(keysym uint32, down bool) error
	// Close cierra la conexion.
	Close() error
}

// Mascara de botones (convencion RFB/X11).
const (
	ButtonLeft   uint8 = 1 << 0
	ButtonMiddle uint8 = 1 << 1
	ButtonRight  uint8 = 1 << 2
	WheelUp      uint8 = 1 << 3
	WheelDown    uint8 = 1 << 4
	WheelLeft    uint8 = 1 << 5
	WheelRight   uint8 = 1 << 6
)

var _ Driver = (*rfb.Session)(nil)

// Open conecta al escritorio con el transporte de la superficie y verifica el
// tamano de pantalla. password nunca aparece en el error.
func Open(ctx context.Context, desktopURL, password string, s Surface, timeout time.Duration) (Driver, error) {
	var d Driver
	switch s.Transport {
	case "rfb":
		sess, err := rfb.Dial(ctx, desktopURL, rfb.Options{
			Password:       password,
			Auth:           s.RFB.Auth,
			Shared:         s.RFB.Shared,
			Encodings:      s.RFB.Encodings,
			ConnectTimeout: timeout,
		})
		if err != nil {
			return nil, err
		}
		d = sess
	default:
		return nil, fmt.Errorf("screen: transporte %q sin driver implementado", s.Transport)
	}
	if err := s.CheckSize(d); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// CheckSize compara el tamano del driver con surface.screen. Con
// trust_server_init se acepta el del servidor; sin el, una diferencia es error
// porque las coordenadas y la escala dependen de ese tamano.
func (s Surface) CheckSize(d Driver) error {
	w, h := d.Size()
	if s.Screen.TrustServerInit {
		return nil
	}
	if w != s.Screen.Width || h != s.Screen.Height {
		return fmt.Errorf("screen: el escritorio mide %dx%d y surface.screen dice %dx%d (trust_server_init=false)",
			w, h, s.Screen.Width, s.Screen.Height)
	}
	return nil
}
