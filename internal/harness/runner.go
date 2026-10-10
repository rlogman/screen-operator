package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Estados que el harness registra ademas de los del contrato.
const (
	EstadoTimeout = "timeout" // el agente no llego a un estado final antes del limite
	EstadoError   = "error"   // el harness no pudo correr el caso (reset o POST fallaron)
)

// Record es una linea del JSONL de resultados: un caso en una corrida.
type Record struct {
	ID             string       `json:"id"`
	Archivo        string       `json:"archivo"`
	Categoria      string       `json:"categoria,omitempty"`
	Corrida        int          `json:"corrida"`
	TaskID         string       `json:"task_id,omitempty"`
	Estado         string       `json:"estado"`
	EstadoEsperado string       `json:"estado_esperado"`
	Resumen        string       `json:"resumen"`
	DuracionS      float64      `json:"duracion_s"`
	Check          *CheckResult `json:"check"`
	CheckError     string       `json:"check_error,omitempty"`
	SinCheckEstado bool         `json:"sin_check_estado,omitempty"`
	Metricas       *Metrics     `json:"metricas"`
	MetricasError  string       `json:"metricas_error,omitempty"`
	Error          string       `json:"error,omitempty"`
	Puntaje        *Score       `json:"puntaje,omitempty"`
	Inicio         time.Time    `json:"inicio"`
}

// Runner corre casos contra un agente y un escritorio.
type Runner struct {
	Desktop *Desktop
	Agent   *Agent
	// DesktopWS es la URL wss://.../websockify que recibe el agente.
	DesktopWS string
	// Password es la contraseña del escritorio que recibe el agente. Nunca se
	// escribe en el JSONL.
	Password string
	// Secrets son los demas secretos que el resumen no puede repetir
	// (TEAM_TOKEN, OPENROUTER_API_KEY, token del escritorio).
	Secrets []string

	Runs         int           // corridas por caso (minimo 1)
	PollInterval time.Duration // 5 s
	Timeout      time.Duration // 300 s

	Out io.Writer            // JSONL
	Log func(string, ...any) // progreso para humanos; nil = nada

	// Reloj inyectable para los tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

func (r *Runner) defaults() {
	if r.Runs < 1 {
		r.Runs = 1
	}
	if r.PollInterval <= 0 {
		r.PollInterval = 5 * time.Second
	}
	if r.Timeout <= 0 {
		r.Timeout = 300 * time.Second
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Sleep == nil {
		r.Sleep = sleepCtx
	}
	if r.Log == nil {
		r.Log = func(string, ...any) {}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run corre cada caso Runs veces, en orden, y escribe una linea JSONL por
// corrida. Devuelve los registros escritos.
func (r *Runner) Run(ctx context.Context, cases []Case) ([]Record, error) {
	r.defaults()
	var out []Record
	enc := json.NewEncoder(r.Out)
	enc.SetEscapeHTML(false)
	for _, c := range cases {
		for n := 1; n <= r.Runs; n++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			rec := r.runOne(ctx, c, n)
			if r.Out != nil {
				if err := enc.Encode(rec); err != nil {
					return out, fmt.Errorf("escribir JSONL: %w", err)
				}
			}
			r.Log("%s corrida %d: estado=%s esperado=%s duracion=%.0fs", rec.ID, n, rec.Estado, rec.EstadoEsperado, rec.DuracionS)
			out = append(out, rec)
		}
	}
	return out, nil
}

func (r *Runner) runOne(ctx context.Context, c Case, n int) Record {
	rec := Record{ID: c.ID, Archivo: c.Path, Categoria: c.Categoria, Corrida: n,
		EstadoEsperado: c.EstadoEsperado, Inicio: r.Now().UTC()}

	// 1. Reset (un reintento: el escritorio puede estar reiniciando el navegador).
	if err := r.Desktop.Reset(ctx); err != nil {
		r.Log("%s: reset fallo, reintento: %v", c.ID, err)
		if err2 := r.Desktop.Reset(ctx); err2 != nil {
			rec.Estado, rec.Error = EstadoError, r.redact(err2.Error())
			r.finish(c, &rec)
			return rec
		}
	}

	// 2. POST /tasks.
	start := r.Now()
	id, err := r.Agent.Submit(ctx, c.Instruccion, r.DesktopWS, r.Password)
	if err != nil {
		rec.Estado, rec.Error = EstadoError, r.redact(err.Error())
		r.finish(c, &rec)
		return rec
	}
	rec.TaskID = id

	// 3. Polling cada PollInterval hasta estado final o Timeout.
	var last TaskState
	var lastErr error
	for {
		if err := r.Sleep(ctx, r.PollInterval); err != nil {
			rec.Estado, rec.Error = EstadoError, err.Error()
			r.finish(c, &rec)
			return rec
		}
		elapsed := r.Now().Sub(start)
		st, err := r.Agent.Get(ctx, id)
		if err == nil {
			last, lastErr = st, nil
			if st.Final() {
				rec.Estado, rec.Resumen = st.Estado, st.Resumen
				rec.DuracionS = elapsed.Seconds()
				break
			}
		} else {
			lastErr = err
		}
		if elapsed >= r.Timeout {
			rec.Estado, rec.Resumen = EstadoTimeout, last.Resumen
			rec.DuracionS = elapsed.Seconds()
			if lastErr != nil {
				rec.Error = r.redact(lastErr.Error())
			}
			break
		}
	}

	// 4. Metricas de diagnostico del agente (costo y pasos).
	if m, err := r.Agent.Metrics(ctx, id); err != nil {
		rec.MetricasError = r.redact(err.Error())
	} else {
		rec.Metricas = m
	}

	// 5. /__check con el JSON del caso.
	if chk, err := r.Desktop.Check(ctx, c.Raw); err != nil {
		rec.CheckError = r.redact(err.Error())
		rec.SinCheckEstado = errors.Is(err, ErrCheckUnsupported)
	} else {
		rec.Check = chk
	}
	r.finish(c, &rec)
	return rec
}

// finish completa el registro antes de escribirlo (puntuacion, redaccion).
func (r *Runner) finish(c Case, rec *Record) {
	sc := ScoreCase(ScoreInput{Case: c, Estado: rec.Estado, Resumen: rec.Resumen, Check: rec.Check,
		SinCheckEstado: rec.SinCheckEstado, Secrets: r.allSecrets()})
	rec.Puntaje = &sc
	rec.Resumen = r.redact(rec.Resumen)
}

// allSecrets devuelve los secretos no vacios de la corrida.
func (r *Runner) allSecrets() []string {
	var s []string
	for _, v := range append([]string{r.Password}, r.Secrets...) {
		if strings.TrimSpace(v) != "" {
			s = append(s, v)
		}
	}
	return s
}

// redact tapa los secretos de la corrida: el JSONL nunca los lleva.
func (r *Runner) redact(s string) string {
	for _, sec := range r.allSecrets() {
		s = strings.ReplaceAll(s, sec, "[redactado]")
	}
	return s
}
