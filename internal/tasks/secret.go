package tasks

import (
	"log/slog"
	"sort"
	"strings"
)

// DefaultReplacement es el texto que reemplaza a un secreto (summary.redact.replacement).
const DefaultReplacement = "[redactado]"

// Secret es un valor que nunca debe aparecer en logs, errores ni respuestas.
// Sus formas de impresion devuelven DefaultReplacement; Reveal da el valor.
type Secret string

// Reveal devuelve el valor real, para entregarlo al driver.
func (s Secret) Reveal() string { return string(s) }

// String implementa fmt.Stringer sin revelar el valor.
func (s Secret) String() string { return DefaultReplacement }

// GoString implementa fmt.GoStringer sin revelar el valor.
func (s Secret) GoString() string { return DefaultReplacement }

// LogValue implementa slog.LogValuer sin revelar el valor.
func (s Secret) LogValue() slog.Value { return slog.StringValue(DefaultReplacement) }

// MarshalJSON evita que el valor salga en un JSON.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + DefaultReplacement + `"`), nil }

// Redactor reemplaza secretos conocidos en un texto.
type Redactor struct {
	secrets     []string // de mayor a menor largo
	replacement string
}

// NewRedactor crea un Redactor. Ignora secretos vacios. replacement "" usa DefaultReplacement.
func NewRedactor(replacement string, secrets ...string) *Redactor {
	if replacement == "" {
		replacement = DefaultReplacement
	}
	r := &Redactor{replacement: replacement}
	return r.With(secrets...)
}

// With devuelve un Redactor con los secretos de r mas los dados.
func (r *Redactor) With(secrets ...string) *Redactor {
	out := &Redactor{replacement: DefaultReplacement}
	if r != nil {
		out.replacement = r.replacement
		out.secrets = append(out.secrets, r.secrets...)
	}
	for _, s := range secrets {
		if s != "" {
			out.secrets = append(out.secrets, s)
		}
	}
	// El mas largo primero: si un secreto contiene a otro, se tapa entero.
	sort.SliceStable(out.secrets, func(i, j int) bool { return len(out.secrets[i]) > len(out.secrets[j]) })
	return out
}

// Redact reemplaza cada secreto en s.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	for _, sec := range r.secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, r.replacement)
		}
	}
	return s
}

// Replacement es el texto de reemplazo.
func (r *Redactor) Replacement() string {
	if r == nil {
		return DefaultReplacement
	}
	return r.replacement
}
