package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// EnvDir es la variable que activa las trazas. Sin definir, no se escribe nada
// en disco.
const EnvDir = "SO_TRACE_DIR"

// ErrClosed lo devuelven Step y Screenshot despues de Close (por ejemplo, un
// agente que sigue corriendo tras el deadline de la tarea).
var ErrClosed = errors.New("trace: recorder cerrado")

// Redactor tapa secretos en un texto (lo implementa *tasks.Redactor).
type Redactor interface {
	Redact(string) string
}

// Step es un paso del agente: fase, accion, razon del modelo, tokens, costo y
// duracion.
type Step struct {
	Phase     string
	Action    string
	Reason    string
	TokensIn  int
	TokensOut int
	CostUSD   float64
	Duration  time.Duration
}

// line es una linea de steps.jsonl.
type line struct {
	Paso          int      `json:"paso"`
	Timestamp     string   `json:"timestamp"`
	Fase          string   `json:"fase"`
	Accion        string   `json:"accion"`
	Razon         string   `json:"razon,omitempty"`
	TokensEntrada int      `json:"tokens_entrada"`
	TokensSalida  int      `json:"tokens_salida"`
	CostoUSD      float64  `json:"costo_usd"`
	DuracionMS    int64    `json:"duracion_ms"`
	Capturas      []string `json:"capturas,omitempty"`
}

// Recorder guarda la traza de una tarea en <dir>/<task_id>/: steps.jsonl (una
// linea por paso) y step-<n>.jpg por cada captura. Con dir vacio no escribe
// nada, pero sigue contando pasos y avisando a OnStep. Un *Recorder nil
// ignora todo. Es seguro para uso concurrente.
type Recorder struct {
	dir    string // "" = sin disco
	red    Redactor
	onStep func(Step)
	now    func() time.Time

	mu      sync.Mutex
	f       *os.File
	n       int      // pasos registrados
	pending []string // capturas del paso en curso
	closed  bool
}

var reTaskID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// New crea el Recorder de una tarea. root "" desactiva la escritura. onStep
// (opcional) recibe cada paso ya redactado: el Task Manager lo usa para las
// metricas de GET /tasks/{id}/metrics.
func New(root, taskID string, red Redactor, onStep func(Step)) (*Recorder, error) {
	r := &Recorder{red: red, onStep: onStep, now: time.Now}
	if root == "" {
		return r, nil
	}
	if !reTaskID.MatchString(taskID) {
		return nil, fmt.Errorf("trace: task_id no apto para nombre de directorio")
	}
	dir := filepath.Join(root, taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "steps.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	r.dir, r.f = dir, f
	return r, nil
}

// Dir es el directorio de la traza ("" si no escribe en disco).
func (r *Recorder) Dir() string {
	if r == nil {
		return ""
	}
	return r.dir
}

// Steps es la cantidad de pasos registrados.
func (r *Recorder) Steps() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func (r *Recorder) redact(s string) string {
	if r.red == nil {
		return s
	}
	return r.red.Redact(s)
}

// Screenshot guarda la captura (JPEG) del paso en curso, es decir, del
// proximo Step: step-<n>.jpg, y step-<n>-<k>.jpg si el paso tiene mas de una.
func (r *Recorder) Screenshot(jpeg []byte) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.dir == "" {
		return nil
	}
	name := fmt.Sprintf("step-%d.jpg", r.n+1)
	if k := len(r.pending); k > 0 {
		name = fmt.Sprintf("step-%d-%d.jpg", r.n+1, k+1)
	}
	if err := os.WriteFile(filepath.Join(r.dir, name), jpeg, 0o600); err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	r.pending = append(r.pending, name)
	return nil
}

// Step registra un paso. Fase, accion y razon se redactan antes de escribir.
func (r *Recorder) Step(s Step) error {
	if r == nil {
		return nil
	}
	s.Phase, s.Action, s.Reason = r.redact(s.Phase), r.redact(s.Action), r.redact(s.Reason)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	r.n++
	l := line{
		Paso: r.n, Timestamp: r.now().UTC().Format(time.RFC3339Nano),
		Fase: s.Phase, Accion: s.Action, Razon: s.Reason,
		TokensEntrada: s.TokensIn, TokensSalida: s.TokensOut, CostoUSD: s.CostUSD,
		DuracionMS: s.Duration.Milliseconds(), Capturas: r.pending,
	}
	r.pending = nil
	var err error
	if r.f != nil {
		b, merr := json.Marshal(l)
		if merr != nil {
			err = merr
		} else if _, werr := r.f.Write(append(b, '\n')); werr != nil {
			err = fmt.Errorf("trace: %w", werr)
		}
	}
	r.mu.Unlock()
	if r.onStep != nil {
		r.onStep(s)
	}
	return err
}

// Close cierra la traza. Despues, Step y Screenshot devuelven ErrClosed.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.f != nil {
		return r.f.Close()
	}
	return nil
}
