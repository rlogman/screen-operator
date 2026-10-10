package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeDesktop hace de servidor falso: registra conexiones, eventos recibidos y cierres.
type fakeDesktop struct {
	mu     sync.Mutex
	order  []string // "dial:<url>", "close:<url>"
	events []recvEvent
	late   int // eventos recibidos por una sesion ya cerrada
}

type recvEvent struct {
	url string
	at  time.Time
}

type fakeSession struct {
	d      *fakeDesktop
	url    string
	closed bool
}

func (d *fakeDesktop) Dial(_ context.Context, req Request) (Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.order = append(d.order, "dial:"+req.DesktopURL)
	return &fakeSession{d: d, url: req.DesktopURL}, nil
}

func (s *fakeSession) Send(ctx context.Context, _ Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.closed {
		s.d.late++
		return errors.New("sesion cerrada")
	}
	s.d.events = append(s.d.events, recvEvent{url: s.url, at: time.Now()})
	return nil
}

func (s *fakeSession) Close() error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.closed = true
	s.d.order = append(s.d.order, "close:"+s.url)
	return nil
}

func (d *fakeDesktop) snapshot() ([]string, []recvEvent, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.order...), append([]recvEvent(nil), d.events...), d.late
}

func newTestManager(t *testing.T, r Runner, d Dialer, b Budget) *Manager {
	t.Helper()
	if b.Total == 0 {
		b = Budget{Total: 300 * time.Second, Margin: 30 * time.Second}
	}
	m := NewManager(Options{Runner: r, Dialer: d, Budget: b})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Shutdown(ctx)
	})
	return m
}

func waitFinal(t *testing.T, m *Manager, id string, within time.Duration) Snapshot {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		s, ok := m.Get(id)
		if !ok {
			t.Fatalf("tarea %s desconocida", id)
		}
		if s.Status != StatusRunning {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("la tarea %s sigue en curso tras %v", id, within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func req(url string) Request {
	return Request{Instruction: "x", DesktopURL: url, DesktopSecrets: []Secret{"P4ss-xyz"}}
}

func TestE3_4_S1_DeadlineFailsTaskAndStopsInput(t *testing.T) {
	// budgets 300 s / 30 s escalados a 3 s / 0,3 s: deadline interno 2,7 s.
	budget := Budget{Total: 3 * time.Second, Margin: 300 * time.Millisecond}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	attempts := make(chan error, 1024)
	// Agente falso que nunca termina: ignora ctx y sigue mandando eventos.
	r := RunnerFunc(func(_ context.Context, job *Job) Result {
		for {
			select {
			case <-stop:
				return Result{Status: StatusDone}
			default:
			}
			err := job.Input.Send(Event{Kind: EventPointer, X: 1, Y: 1})
			select {
			case attempts <- err:
			default:
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	d := &fakeDesktop{}
	m := newTestManager(t, r, d, budget)
	start := time.Now()
	id, err := m.Submit(req("wss://a.example/websockify"))
	if err != nil {
		t.Fatal(err)
	}
	snap := waitFinal(t, m, id, 10*time.Second)
	el := time.Since(start)
	if snap.Status != StatusFailed {
		t.Fatalf("estado = %v, se esperaba fallida", snap.Status)
	}
	if snap.Summary != SummaryTimeout {
		t.Fatalf("resumen = %q, se esperaba el de tiempo agotado", snap.Summary)
	}
	if el < budget.Deadline()-50*time.Millisecond || el > budget.Deadline()+time.Second {
		t.Fatalf("la tarea fallo a los %v, deadline interno %v", el, budget.Deadline())
	}
	// Dejar que el agente siga intentando y verificar que nada mas llega.
	time.Sleep(200 * time.Millisecond)
	_, events, late := d.snapshot()
	if len(events) == 0 {
		t.Fatal("el agente falso no envio ningun evento antes del deadline")
	}
	for _, e := range events {
		if e.at.After(snap.FinishedAt) {
			t.Fatalf("el driver recibio un evento %v despues del deadline (%v)", e.at, snap.FinishedAt)
		}
	}
	if late != 0 {
		t.Fatalf("la sesion cerrada recibio %d eventos", late)
	}
	// Los intentos posteriores fueron rechazados por la Gate.
	rejected := false
	for len(attempts) > 0 {
		if errors.Is(<-attempts, ErrInputClosed) {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("ningun envio posterior fue rechazado con ErrInputClosed")
	}
}

func TestE3_4_S2_InputRejectedAfterReport(t *testing.T) {
	afterErr := make(chan error, 1)
	r := RunnerFunc(func(_ context.Context, job *Job) Result {
		if err := job.Input.Send(Event{Kind: EventKey, Key: 0x61, Down: true}); err != nil {
			t.Errorf("envio antes de reportar: %v", err)
		}
		// Un goroutine rezagado intenta enviar despues de que la tarea reporta.
		go func() {
			time.Sleep(100 * time.Millisecond)
			afterErr <- job.Input.Send(Event{Kind: EventKey, Key: 0x62, Down: true})
		}()
		return Result{Status: StatusDone, Summary: "respuesta: 45"}
	})
	d := &fakeDesktop{}
	m := newTestManager(t, r, d, Budget{})
	id, _ := m.Submit(req("wss://a.example/websockify"))
	snap := waitFinal(t, m, id, 5*time.Second)
	if snap.Status != StatusDone {
		t.Fatalf("estado = %v", snap.Status)
	}
	select {
	case err := <-afterErr:
		if !errors.Is(err, ErrInputClosed) {
			t.Fatalf("el envio posterior devolvio %v, se esperaba ErrInputClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("el envio posterior no retorno")
	}
	_, events, late := d.snapshot()
	if len(events) != 1 || late != 0 {
		t.Fatalf("el servidor falso recibio %d eventos (%d tardios), se esperaba 1", len(events), late)
	}
}

func TestE3_4_S3_NewTaskSameDesktopReplacesOld(t *testing.T) {
	const url = "wss://a.example/websockify"
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls int
	var mu sync.Mutex
	r := RunnerFunc(func(ctx context.Context, job *Job) Result {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(firstStarted)
			// Tarda en soltar: sigue unos ms despues de la cancelacion.
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond)
			return Result{Status: StatusDone, Summary: "no deberia contar"}
		}
		close(secondStarted)
		return Result{Status: StatusDone, Summary: "respuesta: 1"}
	})
	d := &fakeDesktop{}
	m := newTestManager(t, r, d, Budget{})
	id1, _ := m.Submit(req(url))
	<-firstStarted
	id2, _ := m.Submit(req("WSS://A.EXAMPLE/websockify")) // mismo escritorio, otra grafia
	s1 := waitFinal(t, m, id1, 5*time.Second)
	if s1.Status != StatusFailed || s1.Summary != SummaryReplaced {
		t.Fatalf("tarea anterior = %v %q, se esperaba fallida por reemplazo", s1.Status, s1.Summary)
	}
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("la tarea nueva no arranco")
	}
	s2 := waitFinal(t, m, id2, 5*time.Second)
	if s2.Status != StatusDone {
		t.Fatalf("tarea nueva = %v %q", s2.Status, s2.Summary)
	}
	order, _, _ := d.snapshot()
	want := []string{"dial:" + url, "close:" + url, "dial:WSS://A.EXAMPLE/websockify", "close:WSS://A.EXAMPLE/websockify"}
	if len(order) != len(want) {
		t.Fatalf("orden = %v, se esperaba %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("orden = %v, se esperaba %v (la sesion vieja se cierra antes de que la nueva conecte)", order, want)
		}
	}
}

func TestE3_4_S4_DifferentDesktopsRunInParallel(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	both := make(chan struct{})
	go func() { wg.Wait(); close(both) }()
	r := RunnerFunc(func(ctx context.Context, job *Job) Result {
		wg.Done()
		// Cada tarea solo termina cuando la otra tambien arranco: si el manager
		// las serializara, ninguna terminaria antes del timeout.
		select {
		case <-both:
			return Result{Status: StatusDone, Summary: job.Request.DesktopURL}
		case <-time.After(3 * time.Second):
			return Result{Status: StatusFailed, Summary: "serializada"}
		}
	})
	m := newTestManager(t, r, &fakeDesktop{}, Budget{})
	a, _ := m.Submit(req("wss://a.example/websockify"))
	b, _ := m.Submit(req("wss://b.example/websockify"))
	sa := waitFinal(t, m, a, 5*time.Second)
	sb := waitFinal(t, m, b, 5*time.Second)
	if sa.Status != StatusDone || sb.Status != StatusDone {
		t.Fatalf("A=%v %q B=%v %q, se esperaba que ambas terminaran en paralelo", sa.Status, sa.Summary, sb.Status, sb.Summary)
	}
}

func TestSummaryRedactsTaskSecrets(t *testing.T) {
	r := RunnerFunc(func(context.Context, *Job) Result {
		return Result{Status: StatusFailed, Summary: "la clave P4ss-xyz no sirvio"}
	})
	m := newTestManager(t, r, nil, Budget{})
	id, _ := m.Submit(req("wss://a.example/websockify"))
	s := waitFinal(t, m, id, 5*time.Second)
	if s.Summary != "la clave [redactado] no sirvio" {
		t.Fatalf("resumen = %q", s.Summary)
	}
}

func TestRunnerWithoutOutcomeFails(t *testing.T) {
	m := newTestManager(t, RunnerFunc(func(context.Context, *Job) Result { return Result{} }), nil, Budget{})
	id, _ := m.Submit(req("wss://a.example/websockify"))
	if s := waitFinal(t, m, id, 5*time.Second); s.Status != StatusFailed || s.Summary != SummaryNoOutcome {
		t.Fatalf("= %v %q", s.Status, s.Summary)
	}
}
