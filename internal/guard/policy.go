package guard

import (
	"fmt"
	"regexp"
	"strings"

	"screenoperator/internal/config"
)

// Policy es policy.* y summary.redact.* de la configuracion.
type Policy struct {
	Ambiguity struct {
		OnMultipleMatches string `json:"on_multiple_matches"`
		NameIdentifiers   bool   `json:"name_identifiers_in_reason"`
	} `json:"ambiguity"`
	Writes struct {
		CheckExistingBeforeCreate  bool `json:"check_existing_before_create"`
		VerifyAfterWrite           bool `json:"verify_after_write"`
		NeverResubmitWithoutVerify bool `json:"never_resubmit_without_verify"`
	} `json:"writes"`
	Destructive struct {
		BulkChanges string   `json:"bulk_changes"`
		Keywords    []string `json:"keywords"`
		// OrderContextVerbs son verbos de Keywords que en una orden suelen ser
		// contexto ("tras anular una factura...", "la cita cancelada"): no
		// rechazan la orden por palabra clave. Siguen contando para dialogos.
		OrderContextVerbs   []string `json:"order_context_verbs"`
		RequireUniqueTarget bool     `json:"require_unique_target"`
	} `json:"destructive"`
	UnknownValues string `json:"unknown_values"`

	Redact Redaction `json:"-"`
	// CommitLabels son los verbos de los botones que persisten. No hay clave
	// de configuracion para ellos (la duenia de config/ es otra epica): son
	// genericos, no de una app.
	CommitLabels []string `json:"-"`
}

// Redaction es summary.redact.*.
type Redaction struct {
	FromRequestFields  []string `json:"from_request_fields"`
	FromEnv            []string `json:"from_env"`
	ScreenSecretLabels []string `json:"screen_secret_labels"`
	Replacement        string   `json:"replacement"`
}

// DefaultCommitLabels son verbos de botones que persisten o tienen efecto.
// "Crear"/"Nuevo" no estan: suelen abrir un formulario, no guardarlo.
var DefaultCommitLabels = []string{"guardar", "grabar", "exportar", "eliminar", "borrar", "anular", "confirmar", "aceptar",
	"aplicar", "enviar", "save", "delete", "export", "submit", "apply", "confirm"}

// DefaultPolicy es la politica de config/base.yaml.
func DefaultPolicy() Policy {
	var p Policy
	p.Ambiguity.OnMultipleMatches = "fail"
	p.Ambiguity.NameIdentifiers = true
	p.Writes.CheckExistingBeforeCreate = true
	p.Writes.VerifyAfterWrite = true
	p.Writes.NeverResubmitWithoutVerify = true
	p.Destructive.BulkChanges = "fail"
	p.Destructive.Keywords = []string{"eliminar", "borrar", "anular", "cancelar", "vaciar", "todos", "todas"}
	p.Destructive.RequireUniqueTarget = true
	p.UnknownValues = "fail"
	p.Redact = Redaction{FromRequestFields: []string{"escritorio.password"}, FromEnv: []string{"TEAM_TOKEN", "OPENROUTER_API_KEY"},
		ScreenSecretLabels: []string{"clave", "contraseña", "password", "token", "api key", "secreto"}, Replacement: "[redactado]"}
	p.CommitLabels = DefaultCommitLabels
	return p
}

// PolicyFromConfig lee policy.* y summary.redact.*; lo que falte queda como
// en DefaultPolicy.
func PolicyFromConfig(c *config.Config) (Policy, error) {
	p := DefaultPolicy()
	if c == nil {
		return p, nil
	}
	if _, ok := c.Lookup("policy"); ok {
		if err := c.Decode("policy", &p); err != nil {
			return p, fmt.Errorf("policy: %w", err)
		}
	}
	if _, ok := c.Lookup("summary.redact"); ok {
		if err := c.Decode("summary.redact", &p.Redact); err != nil {
			return p, fmt.Errorf("summary.redact: %w", err)
		}
	}
	return p, nil
}

// verbs y scopes separan policy.destructive.keywords: los infinitivos
// (-ar/-er/-ir) son verbos y se comparan por raiz ("elimina", "eliminen");
// el resto ("todos") son palabras de alcance y se comparan exactas.
func (p Policy) verbs() []string {
	skip := map[string]bool{}
	for _, k := range p.Destructive.OrderContextVerbs {
		skip[fold(k)] = true
	}
	var out []string
	for _, k := range p.Destructive.Keywords {
		k = fold(k)
		if isInfinitive(k) && !skip[k] {
			out = append(out, k)
		}
	}
	return out
}

func (p Policy) scopes() []string {
	var out []string
	for _, k := range p.Destructive.Keywords {
		k = fold(k)
		if k != "" && !isInfinitive(k) {
			out = append(out, k)
		}
	}
	return out
}

func isInfinitive(k string) bool {
	return len(k) > 4 && !strings.Contains(k, " ") && (strings.HasSuffix(k, "ar") || strings.HasSuffix(k, "er") || strings.HasSuffix(k, "ir"))
}

// stem es la raiz de un infinitivo: "eliminar" -> "elimin".
func stem(k string) string { return k[:len(k)-2] }

var reWord = regexp.MustCompile(`[\p{L}\p{N}]+`)

// verbHit devuelve el primer verbo de la lista cuya raiz empieza alguna
// palabra de text (ya plegado).
func verbHit(text string, verbs []string) string {
	for _, w := range reWord.FindAllString(text, -1) {
		for _, v := range verbs {
			if strings.HasPrefix(w, stem(v)) {
				return v
			}
		}
	}
	return ""
}

// wordHit devuelve la primera palabra o frase de la lista que aparece entera en text (plegado).
func wordHit(text string, words []string) string {
	padded := " " + strings.Join(reWord.FindAllString(text, -1), " ") + " "
	for _, w := range words {
		ww := strings.Join(reWord.FindAllString(fold(w), -1), " ")
		if ww != "" && strings.Contains(padded, " "+ww+" ") {
			return w
		}
	}
	return ""
}
