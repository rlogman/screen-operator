package tasks

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"screenoperator/internal/trace"
)

// Status es el estado interno de una tarea. La API lo traduce a los valores del
// contrato (contract.response.status_values: en_curso, terminada, fallida).
type Status int

const (
	StatusRunning Status = iota
	StatusDone
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	}
	return "unknown"
}

// Resumenes que fija el Task Manager (no el agente).
const (
	SummaryTimeout   = "Se agotó el tiempo de la tarea antes de terminar; se detuvo toda entrada al escritorio."
	SummaryReplaced  = "Tarea cancelada: reemplazada por una tarea nueva sobre el mismo escritorio."
	SummaryInternal  = "Error interno del agente; la tarea se detuvo sin completarse."
	SummaryDialFail  = "No se pudo conectar al escritorio."
	SummaryShutdown  = "El agente se detuvo antes de terminar la tarea."
	SummaryNoOutcome = "El agente terminó sin reportar un estado final."
)

// Request es una tarea recibida por POST /tasks, ya validada por la API.
type Request struct {
	Instruction string
	DesktopURL  string
	// DesktopSecrets son los valores de contract.request.desktop_secret_fields
	// (la contraseña del escritorio), en el orden de la configuracion.
	DesktopSecrets []Secret
}

// Password devuelve el primer secreto del escritorio (la contraseña VNC) o "".
func (r Request) Password() Secret {
	if len(r.DesktopSecrets) == 0 {
		return ""
	}
	return r.DesktopSecrets[0]
}

// LogValue registra la tarea sin secretos: solo largo de la instruccion y la
// URL sin usuario, clave ni query.
func (r Request) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("instruccion_len", len(r.Instruction)),
		slog.String("escritorio", SafeURL(r.DesktopURL)),
	)
}

// SafeURL devuelve esquema, host y ruta de u, sin usuario, clave, query ni
// fragmento. Si no se puede interpretar devuelve "[url invalida]".
func SafeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[url invalida]"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// Result es lo que reporta el agente al terminar: StatusDone o StatusFailed y
// el resumen para GET /tasks/{id}.
type Result struct {
	Status  Status
	Summary string
}

// Snapshot es el estado visible de una tarea.
type Snapshot struct {
	ID         string
	Status     Status
	Summary    string
	CreatedAt  time.Time
	FinishedAt time.Time // cero mientras esta en curso
}

// Job es lo que recibe el Runner: la tarea, la puerta de entrada al escritorio
// y un logger que ya lleva task_id y redacta los secretos de la tarea.
type Job struct {
	ID      string
	Request Request
	Input   *Gate
	Log     *slog.Logger
	// Usage acumula pasos, tokens y costo de la tarea (GET /tasks/{id}/metrics).
	Usage *UsageMeter
	// Trace guarda los pasos y capturas de la tarea (SO_TRACE_DIR). Nunca es
	// nil cuando lo crea el Manager; sin directorio no escribe en disco pero
	// cada Step igual suma en Usage.
	Trace *trace.Recorder
}

// Runner ejecuta una tarea. Debe respetar ctx: cuando se cancela (deadline,
// reemplazo, apagado) el Task Manager reporta la tarea sin esperar al Runner y
// la Gate rechaza toda entrada posterior. Reportar es retornar.
type Runner interface {
	Run(ctx context.Context, job *Job) Result
}

// RunnerFunc adapta una funcion a Runner.
type RunnerFunc func(ctx context.Context, job *Job) Result

// Run implementa Runner.
func (f RunnerFunc) Run(ctx context.Context, job *Job) Result { return f(ctx, job) }

// EventKind distingue eventos de puntero y de teclado.
type EventKind int

const (
	EventPointer EventKind = iota + 1
	EventKey
)

// Event es un evento de entrada hacia el escritorio (RFB PointerEvent o KeyEvent).
type Event struct {
	Kind    EventKind
	X, Y    int
	Buttons uint8  // mascara de botones (puntero)
	Key     uint32 // keysym (teclado)
	Down    bool   // tecla presionada (teclado)
}

// Session es una conexion abierta a un escritorio. La implementa el adaptador
// del driver RFB (epica 2). Send debe respetar ctx y no bloquear sin limite.
type Session interface {
	Send(ctx context.Context, ev Event) error
	Close() error
}

// Dialer abre la sesion de una tarea. El Task Manager la abre antes de llamar
// al Runner y la cierra al reportar, antes de que otra tarea del mismo
// escritorio conecte.
type Dialer interface {
	Dial(ctx context.Context, req Request) (Session, error)
}

// DialerFunc adapta una funcion a Dialer.
type DialerFunc func(ctx context.Context, req Request) (Session, error)

// Dial implementa Dialer.
func (f DialerFunc) Dial(ctx context.Context, req Request) (Session, error) { return f(ctx, req) }

// Usage es lo que consumio una tarea: pasos del agente, tokens y costo. Es
// diagnostico (GET /tasks/{id}/metrics), no parte del contrato del evento.
type Usage struct {
	Steps     int
	TokensIn  int
	TokensOut int
	CostUSD   float64
}

// UsageMeter acumula el Usage de una tarea. Es seguro para uso concurrente y
// un puntero nil ignora todo.
type UsageMeter struct {
	mu sync.Mutex
	u  Usage
}

// Add suma d al acumulado.
func (m *UsageMeter) Add(d Usage) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.u.Steps += d.Steps
	m.u.TokensIn += d.TokensIn
	m.u.TokensOut += d.TokensOut
	m.u.CostUSD += d.CostUSD
}

// Get devuelve el acumulado.
func (m *UsageMeter) Get() Usage {
	if m == nil {
		return Usage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.u
}
