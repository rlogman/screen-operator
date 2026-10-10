package screen

import (
	"context"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/fakes/rfbserver"
	"screenoperator/internal/rfb"
)

// kiosk carga la configuracion real base <- vnc-kiosk (sin overlay ni perfil
// de app) para que los tests usen los valores que usara el agente.
func kiosk(t *testing.T) (Surface, Perception) {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.ForTask("ws://127.0.0.1/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	s, p, err := FromConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

// fakeDesktop arranca el servidor falso y conecta un Driver RFB real.
func fakeDesktop(t *testing.T, opts rfbserver.Options) (*rfbserver.Server, Driver) {
	t.Helper()
	if opts.Password == "" {
		opts.Password = "secreta1"
	}
	srv := rfbserver.Start(opts)
	t.Cleanup(srv.Close)
	d, err := rfb.Dial(context.Background(), srv.URL(), rfb.Options{Password: opts.Password, Shared: true, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return srv, d
}

// waitEvents espera hasta que el servidor haya registrado n eventos.
func waitEvents(t *testing.T, srv *rfbserver.Server, n int) []rfbserver.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ev := srv.Events(); len(ev) >= n {
			return ev
		}
		time.Sleep(5 * time.Millisecond)
	}
	ev := srv.Events()
	t.Fatalf("el servidor registro %d eventos, se esperaban %d", len(ev), n)
	return nil
}

func keyEvents(ev []rfbserver.Event) []rfbserver.Event {
	var out []rfbserver.Event
	for _, e := range ev {
		if e.Kind == "key" {
			out = append(out, e)
		}
	}
	return out
}
