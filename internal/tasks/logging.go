package tasks

// Logs estructurados: JSON por linea (log/slog), clave "timestamp", campo "fase"
// siempre presente y redaccion de secretos en mensaje y atributos. Los secretos
// del proceso (TEAM_TOKEN, OPENROUTER_API_KEY) se fijan al crear el logger; los
// de cada tarea (contraseña del escritorio) se agregan con WithSecrets.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// Claves de log comunes.
const (
	LogKeyTaskID    = "task_id"
	LogKeyPhase     = "fase"
	LogKeyTimestamp = "timestamp"
	// PhaseDefault se usa cuando una linea no declara fase.
	PhaseDefault = "sistema"
)

// NewLogger crea un logger JSON hacia w que redacta los secretos dados.
func NewLogger(w io.Writer, redactor *Redactor) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				a.Key = LogKeyTimestamp
			}
			return a
		},
	})
	return slog.New(&redactHandler{next: h, r: redactor})
}

// WithSecrets devuelve un logger que ademas redacta los secretos dados. Si l no
// fue creado por NewLogger, se envuelve igual.
func WithSecrets(l *slog.Logger, secrets ...string) *slog.Logger {
	if rh, ok := l.Handler().(*redactHandler); ok {
		c := *rh
		c.r = rh.r.With(secrets...)
		return slog.New(&c)
	}
	return slog.New(&redactHandler{next: l.Handler(), r: NewRedactor("", secrets...)})
}

// RedactorOf devuelve el Redactor de un logger creado por NewLogger/WithSecrets.
func RedactorOf(l *slog.Logger) *Redactor {
	if rh, ok := l.Handler().(*redactHandler); ok {
		return rh.r
	}
	return NewRedactor("")
}

type redactHandler struct {
	next     slog.Handler
	r        *Redactor
	hasPhase bool // algun WithAttrs ya puso fase
}

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Redact(rec.Message), rec.PC)
	hasPhase := h.hasPhase
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == LogKeyPhase {
			hasPhase = true
		}
		out.AddAttrs(h.redactAttr(a))
		return true
	})
	if !hasPhase {
		out.AddAttrs(slog.String(LogKeyPhase, PhaseDefault))
	}
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		if a.Key == LogKeyPhase {
			c.hasPhase = true
		}
		red[i] = h.redactAttr(a)
	}
	c.next = h.next.WithAttrs(red)
	return &c
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.next = h.next.WithGroup(name)
	return &c
}

func (h *redactHandler) redactAttr(a slog.Attr) slog.Attr {
	a.Key = h.r.Redact(a.Key)
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.r.Redact(v.String()))
	case slog.KindGroup:
		gs := v.Group()
		red := make([]slog.Attr, len(gs))
		for i, g := range gs {
			red[i] = h.redactAttr(g)
		}
		a.Value = slog.GroupValue(red...)
	case slog.KindAny:
		// Errores, structs, mapas: se imprimen y, si contienen un secreto, se
		// reemplazan por su texto redactado.
		s := fmt.Sprint(v.Any())
		if rs := h.r.Redact(s); rs != s {
			a.Value = slog.StringValue(rs)
		} else {
			a.Value = v
		}
	default:
		a.Value = v
	}
	return a
}
