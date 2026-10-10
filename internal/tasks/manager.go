package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Causas de cancelacion de una tarea.
var (
	ErrTimeout  = errors.New("deadline interno de la tarea")
	ErrReplaced = errors.New("reemplazada por una tarea nueva")
	ErrShutdown = errors.New("apagado del agente")
	errReported = errors.New("la tarea ya reporto")
)

// Budget es el presupuesto de tiempo: el deadline interno es Total - Margin
// (budgets.task_deadline_s - budgets.safety_margin_s), contado desde POST /tasks.
type Budget struct {
	Total  time.Duration
	Margin time.Duration
}

// Deadline es el tiempo interno disponible. Nunca es menor que un segundo.
func (b Budget) Deadline() time.Duration {
	d := b.Total - b.Margin
	if d < time.Second {
		d = time.Second
	}
	return d
}

// Options configura un Manager.
type Options struct {
	Runner Runner
	Dialer Dialer // nil: sesion sin driver (todo envio falla con ErrNoDriver)
	Budget Budget
	Logger *slog.Logger // nil: descarta
	// Redactor con los secretos del proceso; se le agregan los de cada tarea para
	// redactar resumenes. nil: se toma el del Logger.
	Redactor *Redactor
	NewID    func() string
	Now      func() time.Time
}

// Manager ejecuta una tarea por escritorio a la vez, aplica el deadline interno
// y cierra la entrada al escritorio en cuanto la tarea reporta.
type Manager struct {
	o      Options
	base   context.Context
	stop   context.CancelCauseFunc
	mu     sync.Mutex
	tasks  map[string]*task
	slots  map[string]*slot // por escritorio: la ultima tarea aceptada
	wg     sync.WaitGroup
	closed bool
}

type task struct {
	id      string
	req     Request
	log     *slog.Logger
	red     *Redactor
	created time.Time
	// protegidos por Manager.mu
	status   Status
	summary  string
	finished time.Time
}

type slot struct {
	cancel context.CancelCauseFunc
	done   chan struct{} // se cierra cuando la sesion de la tarea ya esta cerrada
}

// NewManager crea un Manager. Runner es obligatorio.
func NewManager(o Options) *Manager {
	if o.Runner == nil {
		panic("tasks: Options.Runner es obligatorio")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if o.Redactor == nil {
		o.Redactor = RedactorOf(o.Logger)
	}
	if o.NewID == nil {
		o.NewID = randomID
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	base, stop := context.WithCancelCause(context.Background())
	return &Manager{o: o, base: base, stop: stop, tasks: map[string]*task{}, slots: map[string]*slot{}}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand no falla en plataformas soportadas
	}
	return hex.EncodeToString(b[:])
}

// DesktopKey normaliza la URL del escritorio para identificar "el mismo
// escritorio": esquema y host en minusculas, ruta; sin usuario ni query.
func DesktopKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.Path
}

// Submit registra la tarea en curso y la encola en el worker de su escritorio.
// No bloquea: retorna en microsegundos aunque la tarea anterior del mismo
// escritorio tarde en soltar la sesion.
func (m *Manager) Submit(req Request) (string, error) {
	now := m.o.Now()
	secrets := make([]string, 0, len(req.DesktopSecrets))
	for _, s := range req.DesktopSecrets {
		secrets = append(secrets, s.Reveal())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", ErrShutdown
	}
	id := m.o.NewID()
	for m.tasks[id] != nil {
		id = m.o.NewID()
	}
	t := &task{
		id:      id,
		req:     req,
		log:     WithSecrets(m.o.Logger, secrets...).With(LogKeyTaskID, id),
		red:     m.o.Redactor.With(secrets...),
		created: now,
		status:  StatusRunning,
	}
	m.tasks[id] = t

	ctx1, cancel := context.WithCancelCause(m.base)
	ctx, cancelDeadline := context.WithTimeoutCause(ctx1, m.o.Budget.Deadline(), ErrTimeout)
	key := DesktopKey(req.DesktopURL)
	prev := m.slots[key]
	s := &slot{cancel: cancel, done: make(chan struct{})}
	m.slots[key] = s
	if prev != nil {
		// Se cancela ya: su Gate deja de aceptar eventos desde este momento.
		prev.cancel(ErrReplaced)
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancelDeadline()
		m.work(ctx, cancel, t, prev, s, key)
	}()
	t.log.Info("tarea recibida", LogKeyPhase, "recepcion", "tarea", req,
		"deadline_ms", m.o.Budget.Deadline().Milliseconds(), "reemplaza_anterior", prev != nil)
	return id, nil
}

// Get devuelve el estado de una tarea.
func (m *Manager) Get(id string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{ID: t.id, Status: t.status, Summary: t.summary, CreatedAt: t.created, FinishedAt: t.finished}, true
}

// Shutdown cancela todas las tareas y espera a que sus sesiones se cierren o a ctx.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stop(ErrShutdown)
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) work(ctx context.Context, cancel context.CancelCauseFunc, t *task, prev, s *slot, key string) {
	start := m.o.Now()
	defer func() {
		m.mu.Lock()
		if m.slots[key] == s {
			delete(m.slots, key)
		}
		m.mu.Unlock()
		close(s.done)
	}()

	// 1. Esperar a que la tarea anterior del mismo escritorio cierre su sesion.
	if prev != nil {
		t.log.Info("esperando que la tarea anterior suelte el escritorio", LogKeyPhase, "cola")
		select {
		case <-prev.done:
		case <-ctx.Done():
			m.finish(t, causeResult(ctx), start)
			return
		}
	}
	if ctx.Err() != nil {
		m.finish(t, causeResult(ctx), start)
		return
	}

	// 2. Conectar.
	var sess Session = noDriverSession{}
	if m.o.Dialer != nil {
		t.log.Info("conectando al escritorio", LogKeyPhase, "conexion")
		var err error
		sess, err = m.o.Dialer.Dial(ctx, t.req)
		if err != nil {
			if ctx.Err() != nil {
				m.finish(t, causeResult(ctx), start)
				return
			}
			t.log.Warn("no se pudo conectar", LogKeyPhase, "conexion", "error", err)
			m.finish(t, Result{Status: StatusFailed, Summary: SummaryDialFail}, start)
			return
		}
	}
	gate := newGate(ctx, sess)

	// 3. Ejecutar el agente. Un panic se convierte en fallida generica.
	job := &Job{ID: t.id, Request: t.req, Input: gate, Log: t.log}
	resCh := make(chan Result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.log.Error("panic en el agente", LogKeyPhase, "agente",
					"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				resCh <- Result{Status: StatusFailed, Summary: SummaryInternal}
			}
		}()
		resCh <- m.o.Runner.Run(ctx, job)
	}()
	var res Result
	select {
	case res = <-resCh:
	case <-ctx.Done():
		res = causeResult(ctx)
	}

	// 4. Corte duro: nada de entrada despues de reportar, sesion cerrada antes
	// de publicar el estado y antes de que la tarea siguiente conecte.
	gate.Close()
	cancel(errReported)
	if err := sess.Close(); err != nil {
		t.log.Warn("error al cerrar la sesion", LogKeyPhase, "cierre", "error", err)
	}
	sent, denied := gate.Counts()
	t.log.Info("sesion cerrada", LogKeyPhase, "cierre", "eventos_enviados", sent, "eventos_rechazados", denied)
	m.finish(t, res, start)
}

// causeResult traduce la causa de cancelacion a un resultado fallido.
func causeResult(ctx context.Context) Result {
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, ErrTimeout):
		return Result{Status: StatusFailed, Summary: SummaryTimeout}
	case errors.Is(cause, ErrReplaced):
		return Result{Status: StatusFailed, Summary: SummaryReplaced}
	case errors.Is(cause, ErrShutdown):
		return Result{Status: StatusFailed, Summary: SummaryShutdown}
	default:
		return Result{Status: StatusFailed, Summary: SummaryInternal}
	}
}

func (m *Manager) finish(t *task, res Result, start time.Time) {
	if res.Status != StatusDone && res.Status != StatusFailed {
		res = Result{Status: StatusFailed, Summary: SummaryNoOutcome}
	}
	summary := t.red.Redact(res.Summary)
	now := m.o.Now()
	m.mu.Lock()
	t.status, t.summary, t.finished = res.Status, summary, now
	m.mu.Unlock()
	t.log.Info("tarea reportada", LogKeyPhase, "reporte", "estado", res.Status.String(),
		"duracion_ms", now.Sub(start).Milliseconds())
}
