package agent

// Adaptador del Driver de pantalla (epica 2) al Dialer/Session del Task
// Manager (epica 3). Resuelve el arrastre de so-epic-3:
//   - Dial respeta ctx: si el contexto vence mientras el driver conecta, Dial
//     retorna enseguida y el driver que llegue tarde se cierra en segundo plano.
//   - Send y Close nunca bloquean sin limite: cada envio tiene un plazo
//     (SendTimeout) y respeta ctx; si el driver no responde se cierra, lo que
//     desbloquea la escritura pendiente.
//   - Un panic del driver en Dial, Send o Close se convierte en error.
//   - Ningun error lleva la URL cruda (usuario, clave, query) ni la contraseña.

import (
	"context"
	"errors"
	"fmt"
	"image"
	"net/url"
	"strings"
	"sync"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/rfb"
	"screenoperator/internal/screen"
	"screenoperator/internal/tasks"
)

// Plazos por defecto del adaptador.
const (
	DefaultConnectTimeout = 15 * time.Second
	DefaultSendTimeout    = 3 * time.Second
	DefaultCloseTimeout   = 3 * time.Second
	DefaultDialAttempts   = 3
	DefaultDialRetryWait  = 3 * time.Second
)

// ErrSendTimeout: el driver no acepto un evento dentro de SendTimeout.
var ErrSendTimeout = errors.New("el escritorio no acepto el evento a tiempo")

// OpenFunc abre un screen.Driver (screen.Open en produccion; un falso en tests).
type OpenFunc func(ctx context.Context, desktopURL, password string, s screen.Surface, timeout time.Duration) (screen.Driver, error)

// DriverDialer implementa tasks.Dialer sobre screen.Open.
type DriverDialer struct {
	Loader         *config.Loader
	Open           OpenFunc // nil = screen.Open
	ConnectTimeout time.Duration
	SendTimeout    time.Duration
	CloseTimeout   time.Duration
	Attempts       int           // intentos de conexion (por defecto 3; nunca tras auth rechazada)
	RetryWait      time.Duration // espera entre intentos
}

// DriverSession implementa tasks.Session sobre un screen.Driver y expone el
// driver para lecturas (capturas). La entrada debe pasar por la Gate.
type DriverSession struct {
	d            screen.Driver
	cfg          *config.Config
	surface      screen.Surface
	perception   screen.Perception
	sendTimeout  time.Duration
	closeTimeout time.Duration
	scrub        func(string) string
	closeOnce    sync.Once
	closeErr     error
}

// Dial abre la sesion del escritorio de la tarea.
func (dd *DriverDialer) Dial(ctx context.Context, req tasks.Request) (sess tasks.Session, err error) {
	scrub := scrubber(req.DesktopURL, req.Password().Reveal())
	defer func() {
		if r := recover(); r != nil {
			sess, err = nil, errors.New("panic al conectar con el escritorio")
		}
	}()
	if dd.Loader == nil {
		return nil, errors.New("adaptador sin configuracion")
	}
	cfg, err := dd.Loader.ForTask(req.DesktopURL, "")
	if err != nil {
		return nil, errors.New(scrub(err.Error()))
	}
	surf, perc, err := screen.FromConfig(cfg)
	if err != nil {
		return nil, errors.New(scrub(err.Error()))
	}
	open := dd.Open
	if open == nil {
		open = screen.Open
	}
	timeout := orDur(dd.ConnectTimeout, DefaultConnectTimeout)

	attempts := dd.Attempts
	if attempts <= 0 {
		attempts = DefaultDialAttempts
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			// Tras un /__reset el kiosko reinicia el navegador y el VNC tarda en
			// aceptar: se reintenta con espera, dentro del plazo de la tarea.
			t := time.NewTimer(orDur(dd.RetryWait, DefaultDialRetryWait))
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return nil, fmt.Errorf("conexion al escritorio cancelada: %w", context.Cause(ctx))
			}
		}
		d, err := dd.attempt(ctx, open, req, surf, timeout)
		if err == nil {
			return &DriverSession{d: d, cfg: cfg, surface: surf, perception: perc,
				sendTimeout: orDur(dd.SendTimeout, DefaultSendTimeout), closeTimeout: orDur(dd.CloseTimeout, DefaultCloseTimeout),
				scrub: scrub}, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("conexion al escritorio cancelada: %w", context.Cause(ctx))
		}
		last = err
		if errors.Is(err, rfb.ErrAuth) {
			break // una contraseña rechazada no mejora reintentando
		}
	}
	return nil, fmt.Errorf("no se pudo conectar al escritorio %s: %s", tasks.SafeURL(req.DesktopURL), scrub(last.Error()))
}

// attempt hace un intento de conexion que respeta ctx aunque el driver no lo haga.
func (dd *DriverDialer) attempt(ctx context.Context, open OpenFunc, req tasks.Request, surf screen.Surface, timeout time.Duration) (screen.Driver, error) {
	type result struct {
		d   screen.Driver
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: errors.New("panic al conectar con el escritorio")}
			}
		}()
		d, err := open(ctx, req.DesktopURL, req.Password().Reveal(), surf, timeout)
		ch <- result{d, err}
	}()
	select {
	case r := <-ch:
		return r.d, r.err
	case <-ctx.Done():
		// El driver que llegue tarde se cierra sin que nadie lo espere.
		go func() {
			if r := <-ch; r.d != nil {
				_ = r.d.Close()
			}
		}()
		return nil, context.Cause(ctx)
	}
}

func orDur(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Driver devuelve el driver para lecturas (Capture, Size).
func (s *DriverSession) Driver() screen.Driver { return s.d }

// Config es la configuracion resuelta de la superficie (sin perfil de app).
func (s *DriverSession) Config() *config.Config { return s.cfg }

// Surface y Perception de la sesion.
func (s *DriverSession) Surface() screen.Surface       { return s.surface }
func (s *DriverSession) Perception() screen.Perception { return s.perception }

// Send entrega un evento al driver con plazo y respetando ctx.
func (s *DriverSession) Send(ctx context.Context, ev tasks.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- errors.New("panic del driver al enviar un evento")
			}
		}()
		switch ev.Kind {
		case tasks.EventPointer:
			done <- s.d.Pointer(ev.X, ev.Y, ev.Buttons)
		case tasks.EventKey:
			done <- s.d.Key(ev.Key, ev.Down)
		default:
			done <- fmt.Errorf("evento de tipo %d desconocido", ev.Kind)
		}
	}()
	t := time.NewTimer(s.sendTimeout)
	defer t.Stop()
	select {
	case err := <-done:
		if err != nil {
			return errors.New(s.scrub(err.Error()))
		}
		return nil
	case <-t.C:
		// Un driver colgado no puede retener la Gate: se cierra la conexion,
		// lo que desbloquea la escritura pendiente.
		_ = s.Close()
		return ErrSendTimeout
	case <-ctx.Done():
		_ = s.Close()
		return ctx.Err()
	}
}

// Close cierra el driver una sola vez, con plazo y sin propagar panics.
func (s *DriverSession) Close() error {
	s.closeOnce.Do(func() {
		done := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- errors.New("panic del driver al cerrar")
				}
			}()
			done <- s.d.Close()
		}()
		t := time.NewTimer(s.closeTimeout)
		defer t.Stop()
		select {
		case err := <-done:
			if err != nil {
				s.closeErr = errors.New(s.scrub(err.Error()))
			}
		case <-t.C:
			s.closeErr = errors.New("el driver no cerro a tiempo")
		}
	})
	return s.closeErr
}

// scrubber quita de un texto la URL cruda, su usuario, clave, query y la
// contraseña del escritorio.
func scrubber(rawURL, password string) func(string) string {
	var secrets []string
	add := func(s string) {
		if s != "" && len(s) >= 3 {
			secrets = append(secrets, s)
		}
	}
	add(strings.TrimSpace(rawURL))
	add(password)
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		if u.User != nil {
			add(u.User.String())
			add(u.User.Username())
			if p, ok := u.User.Password(); ok {
				add(p)
			}
		}
		add(u.RawQuery)
		for _, vs := range u.Query() {
			for _, v := range vs {
				add(v)
			}
		}
	}
	safe := tasks.SafeURL(rawURL)
	return func(s string) string {
		for _, sec := range secrets {
			if sec == strings.TrimSpace(rawURL) {
				s = strings.ReplaceAll(s, sec, safe)
				continue
			}
			s = strings.ReplaceAll(s, sec, "[redactado]")
		}
		return s
	}
}

// gatedDriver es el screen.Driver que ve el agente: lee del driver real y
// manda toda entrada por la Gate de la tarea (que la corta al reportar).
type gatedDriver struct {
	d    screen.Driver
	gate *tasks.Gate
}

func (g gatedDriver) Size() (int, int) { return g.d.Size() }
func (g gatedDriver) Name() string     { return g.d.Name() }
func (g gatedDriver) Capture(ctx context.Context) (*image.RGBA, error) {
	return g.d.Capture(ctx)
}
func (g gatedDriver) Pointer(x, y int, buttons uint8) error {
	return g.gate.Send(tasks.Event{Kind: tasks.EventPointer, X: x, Y: y, Buttons: buttons})
}
func (g gatedDriver) Key(keysym uint32, down bool) error {
	return g.gate.Send(tasks.Event{Kind: tasks.EventKey, Key: keysym, Down: down})
}

// Close no cierra nada: la sesion la cierra el Task Manager al reportar.
func (g gatedDriver) Close() error { return nil }
