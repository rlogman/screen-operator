package guard

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Redactor reemplaza secretos en un texto (7.5): los valores de la tarea
// (summary.redact.from_request_fields), los del entorno
// (summary.redact.from_env), los valores vistos en pantalla junto a una
// etiqueta sensible (summary.redact.screen_secret_labels) y cualquier
// "etiqueta sensible: valor" en el texto mismo. Es seguro para uso concurrente.
type Redactor struct {
	mu          sync.Mutex
	secrets     []string
	labels      []string
	replacement string
	rePair      *regexp.Regexp
}

// minSecret evita tapar valores triviales ("Si", "No") como secretos.
const minSecret = 4

// NewRedactor arma el redactor con los secretos conocidos de antemano.
func NewRedactor(r Redaction, secrets ...string) *Redactor {
	rep := r.Replacement
	if rep == "" {
		rep = "[redactado]"
	}
	red := &Redactor{replacement: rep}
	var alts []string
	for _, l := range r.ScreenSecretLabels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		red.labels = append(red.labels, fold(l))
		alts = append(alts, regexp.QuoteMeta(l), regexp.QuoteMeta(unaccent(l)))
	}
	if len(alts) > 0 {
		// "Clave de integración: SGC-1234" -> "Clave de integración: [redactado]"
		red.rePair = regexp.MustCompile(`(?i)((?:` + strings.Join(alts, "|") + `)[^:=\n]{0,40}?\s*[:=]\s*)([^\s,;]+)`)
	}
	red.Add(secrets...)
	return red
}

// Add agrega secretos.
func (r *Redactor) Add(secrets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len(s) < minSecret || s == r.replacement {
			continue
		}
		dup := false
		for _, x := range r.secrets {
			if x == s {
				dup = true
				break
			}
		}
		if !dup {
			r.secrets = append(r.secrets, s)
		}
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

// isLabel dice si un texto es (o contiene como palabra) una etiqueta sensible.
func (r *Redactor) isLabel(s string) bool {
	return wordHit(fold(s), r.labels) != ""
}

// LearnRows aprende como secretos los valores de una columna con etiqueta
// sensible y, en tablas campo/valor, la celda que sigue a una etiqueta
// sensible.
func (r *Redactor) LearnRows(columns []string, rows []map[string]string) {
	var learned []string
	for _, row := range rows {
		for col, v := range row {
			if r.isLabel(col) {
				learned = append(learned, v)
			}
		}
		if len(columns) > 1 {
			for i := 0; i+1 < len(columns); i++ {
				if lab, ok := row[columns[i]]; ok && r.isLabel(lab) {
					learned = append(learned, row[columns[i+1]])
				}
			}
		}
	}
	r.Add(learned...)
}

// LearnText aprende los valores de pares "etiqueta: valor" de un texto.
func (r *Redactor) LearnText(s string) {
	if r.rePair == nil {
		return
	}
	var learned []string
	for _, m := range r.rePair.FindAllStringSubmatch(s, -1) {
		learned = append(learned, m[2])
	}
	r.Add(learned...)
}

// Redact reemplaza cada secreto conocido y cada valor junto a una etiqueta sensible.
func (r *Redactor) Redact(s string) string {
	if r == nil || s == "" {
		return s
	}
	r.mu.Lock()
	secrets := r.secrets
	r.mu.Unlock()
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, r.replacement)
		}
	}
	if r.rePair != nil {
		s = r.rePair.ReplaceAllStringFunc(s, func(m string) string {
			sub := r.rePair.FindStringSubmatch(m)
			if sub[2] == r.replacement || strings.HasPrefix(sub[2], r.replacement) {
				return m
			}
			return sub[1] + r.replacement
		})
	}
	return s
}
