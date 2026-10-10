package tasks

import (
	"context"
	"testing"
	"time"
)

type panicSession struct{}

func (panicSession) Send(context.Context, Event) error { return nil }
func (panicSession) Close() error                      { panic("boom close") }

// Arrastre so-epic-3: el recover de work() cubre Dial y Close, no solo el Runner.
func TestWorkRecoversPanicInDialAndClose(t *testing.T) {
	done := RunnerFunc(func(context.Context, *Job) Result { return Result{Status: StatusDone, Summary: "ok"} })

	// Panic en Dial: la tarea termina fallida con el resumen de conexion.
	m := newTestManager(t, done, DialerFunc(func(context.Context, Request) (Session, error) { panic("boom dial") }), Budget{})
	id, err := m.Submit(Request{Instruction: "x", DesktopURL: "wss://a/websockify"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitFinal(t, m, id, 2*time.Second)
	if snap.Status != StatusFailed || snap.Summary != SummaryDialFail {
		t.Fatalf("panic en Dial: %v %q", snap.Status, snap.Summary)
	}

	// Panic en Close: el estado del Runner se publica igual y el manager sigue vivo.
	m2 := newTestManager(t, done, DialerFunc(func(context.Context, Request) (Session, error) { return panicSession{}, nil }), Budget{})
	id, err = m2.Submit(Request{Instruction: "x", DesktopURL: "wss://b/websockify"})
	if err != nil {
		t.Fatal(err)
	}
	snap = waitFinal(t, m2, id, 2*time.Second)
	if snap.Status != StatusDone || snap.Summary != "ok" {
		t.Fatalf("panic en Close: %v %q", snap.Status, snap.Summary)
	}
	// Otra tarea del mismo escritorio puede conectar despues.
	id, _ = m2.Submit(Request{Instruction: "y", DesktopURL: "wss://b/websockify"})
	if snap := waitFinal(t, m2, id, 2*time.Second); snap.Status != StatusDone {
		t.Fatalf("segunda tarea: %v", snap.Status)
	}
}
