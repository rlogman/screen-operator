package agent

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/fakes"
	"screenoperator/internal/screen"
	"screenoperator/internal/tasks"
)

func testLoader(t *testing.T) *config.Loader {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func req(url, pw string) tasks.Request {
	return tasks.Request{Instruction: "x", DesktopURL: url, DesktopSecrets: []tasks.Secret{tasks.Secret(pw)}}
}

// Arrastre so-epic-3 (1): Dial respeta ctx aunque el driver no responda.
func TestAdapterDialRespectsContext(t *testing.T) {
	release := make(chan struct{})
	late := fakes.NewDriver(64, 40, color.RGBA{})
	dd := &DriverDialer{Loader: testLoader(t), Open: func(ctx context.Context, _, _ string, _ screen.Surface, _ time.Duration) (screen.Driver, error) {
		<-release // un driver que ignora ctx
		return late, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	s, err := dd.Dial(ctx, req("wss://escritorio.test/websockify", "Pw-dial-1"))
	if err == nil || s != nil {
		t.Fatalf("Dial debia fallar al vencer ctx: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Dial tardo %v con ctx de 80 ms", d)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for !late.Closed() {
		if time.Now().After(deadline) {
			t.Fatal("el driver que llego tarde no se cerro")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Arrastre so-epic-3 (2): Send y Close no se cuelgan con un driver bloqueado,
// y el Task Manager reporta a tiempo aunque el agente este enviando.
func TestAdapterSendAndCloseDoNotHang(t *testing.T) {
	d := fakes.NewDriver(1280, 800, color.RGBA{})
	d.BlockInput = make(chan struct{}) // nunca se libera: Pointer/Key bloquean
	dd := &DriverDialer{Loader: testLoader(t), SendTimeout: 100 * time.Millisecond, CloseTimeout: 100 * time.Millisecond,
		Open: func(context.Context, string, string, screen.Surface, time.Duration) (screen.Driver, error) { return d, nil }}
	s, err := dd.Dial(context.Background(), req("wss://escritorio.test/websockify", "Pw-send-2"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = s.Send(context.Background(), tasks.Event{Kind: tasks.EventPointer, X: 1, Y: 1, Buttons: 1})
	if !errors.Is(err, ErrSendTimeout) {
		t.Fatalf("Send = %v, se esperaba ErrSendTimeout", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("Send tardo %v", el)
	}
	start = time.Now()
	_ = s.Close()
	if el := time.Since(start); el > time.Second {
		t.Fatalf("Close tardo %v", el)
	}
	// Send respeta ctx.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Send(ctx, tasks.Event{Kind: tasks.EventKey, Key: 'a', Down: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send con ctx cancelado = %v", err)
	}

	// Extremo a extremo: un agente que envia mientras vence el deadline. El
	// reporte llega a tiempo aunque el driver este colgado (la Gate sostiene su
	// mutex durante Send, pero Send esta acotado).
	d2 := fakes.NewDriver(1280, 800, color.RGBA{})
	d2.BlockInput = make(chan struct{})
	dd2 := &DriverDialer{Loader: testLoader(t), SendTimeout: 150 * time.Millisecond, CloseTimeout: 150 * time.Millisecond,
		Open: func(context.Context, string, string, screen.Surface, time.Duration) (screen.Driver, error) { return d2, nil }}
	runner := tasks.RunnerFunc(func(ctx context.Context, job *tasks.Job) tasks.Result {
		for ctx.Err() == nil {
			_ = job.Input.Send(tasks.Event{Kind: tasks.EventPointer, X: 5, Y: 5})
		}
		return tasks.Result{Status: tasks.StatusDone, Summary: "x"}
	})
	m := tasks.NewManager(tasks.Options{Runner: runner, Dialer: dd2, Budget: tasks.Budget{Total: 400 * time.Millisecond}})
	defer m.Shutdown(context.Background())
	id, err := m.Submit(req("wss://escritorio.test/websockify", "Pw-send-3"))
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	for {
		snap, _ := m.Get(id)
		if snap.Status != tasks.StatusRunning {
			if snap.Status != tasks.StatusFailed || snap.Summary != tasks.SummaryTimeout {
				t.Fatalf("estado %v %q", snap.Status, snap.Summary)
			}
			break
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("el reporte se colgo con el driver bloqueado")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type panicDriver struct{ *fakes.Driver }

func (panicDriver) Close() error                  { panic("boom close") }
func (panicDriver) Pointer(int, int, uint8) error { panic("boom pointer") }

// Arrastre so-epic-3 (3): panic del driver en Dial, Send o Close = error.
func TestAdapterRecoversDriverPanics(t *testing.T) {
	dd := &DriverDialer{Loader: testLoader(t), Open: func(context.Context, string, string, screen.Surface, time.Duration) (screen.Driver, error) {
		panic("boom dial")
	}}
	if _, err := dd.Dial(context.Background(), req("wss://escritorio.test/websockify", "Pw-panic-4")); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("Dial con panic = %v", err)
	}
	pd := panicDriver{fakes.NewDriver(64, 40, color.RGBA{})}
	dd.Open = func(context.Context, string, string, screen.Surface, time.Duration) (screen.Driver, error) { return pd, nil }
	s, err := dd.Dial(context.Background(), req("wss://escritorio.test/websockify", "Pw-panic-4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), tasks.Event{Kind: tasks.EventPointer, X: 1, Y: 1}); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("Send con panic = %v", err)
	}
	if err := s.Close(); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("Close con panic = %v", err)
	}
}

// Arrastre so-epic-3 (4): los errores del driver no llevan la URL cruda
// (usuario:clave@, query) ni la contraseña.
func TestAdapterErrorsHideRawURL(t *testing.T) {
	raw := "wss://operador:Clave-URL-55@escritorio.test/websockify?token=Tk-query-66"
	dd := &DriverDialer{Loader: testLoader(t), Open: func(_ context.Context, u, pw string, _ screen.Surface, _ time.Duration) (screen.Driver, error) {
		return nil, fmt.Errorf("fallo al abrir %s con %s", u, pw)
	}}
	_, err := dd.Dial(context.Background(), req(raw, "Pw-secreta-77"))
	if err == nil {
		t.Fatal("se esperaba error")
	}
	for _, leak := range []string{"Clave-URL-55", "operador:", "Tk-query-66", "token=", "Pw-secreta-77"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("el error filtra %q: %v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "wss://escritorio.test/websockify") {
		t.Fatalf("el error deberia nombrar la URL segura: %v", err)
	}
	// Una URL con esquema no soportado tampoco se repite cruda.
	_, err = dd.Dial(context.Background(), req("https://u:Clave-URL-55@escritorio.test/x?k=Tk-query-66", "Pw-secreta-77"))
	if err == nil || strings.Contains(err.Error(), "Clave-URL-55") || strings.Contains(err.Error(), "Tk-query-66") {
		t.Fatalf("error = %v", err)
	}
}
