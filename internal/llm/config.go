package llm

import (
	"fmt"
	"strings"
	"time"

	"screenoperator/internal/config"
)

// Role es models.roles.<rol> de la configuracion.
type Role struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	PriceIn     float64 `json:"price_in_usd_per_mtok"`
	PriceOut    float64 `json:"price_out_usd_per_mtok"`
	Reasoning   string  `json:"reasoning_effort"`
}

// Models es models.* de la configuracion.
type Models struct {
	Provider     string          `json:"provider"`
	BaseURL      string          `json:"base_url"`
	APIKeyEnv    string          `json:"api_key_env"`
	CallTimeoutS int             `json:"call_timeout_s"`
	MaxRetries   *int            `json:"max_retries"`
	Roles        map[string]Role `json:"roles"`
}

// ModelsFromConfig lee models.*.
func ModelsFromConfig(c *config.Config) (Models, error) {
	var m Models
	if err := c.Decode("models", &m); err != nil {
		return m, fmt.Errorf("llm: models: %w", err)
	}
	if _, ok := m.Roles["actor"]; !ok {
		return m, fmt.Errorf("llm: falta models.roles.actor")
	}
	return m, nil
}

// Role devuelve el rol pedido o, si no existe, el actor.
func (m Models) Role(name string) Role {
	if r, ok := m.Roles[name]; ok {
		return r
	}
	return m.Roles["actor"]
}

// Prices arma la tabla de precios por modelo desde los roles.
func (m Models) Prices() map[string]Price {
	out := map[string]Price{}
	for _, r := range m.Roles {
		if r.PriceIn > 0 || r.PriceOut > 0 {
			out[r.Model] = Price{InPerM: r.PriceIn, OutPerM: r.PriceOut}
		}
	}
	return out
}

// ClientOptions arma las opciones del cliente. La API key sale del entorno
// (models.api_key_env); sin ella es error, sin nombrar su valor.
func (m Models) ClientOptions(getenv func(string) string) (Options, error) {
	key := strings.TrimSpace(getenv(m.APIKeyEnv))
	if key == "" {
		return Options{}, fmt.Errorf("llm: falta la variable de entorno %s", m.APIKeyEnv)
	}
	o := Options{BaseURL: m.BaseURL, APIKey: key, Prices: m.Prices(), MaxRetries: DefaultMaxRetries}
	if m.CallTimeoutS > 0 {
		o.CallTimeout = time.Duration(m.CallTimeoutS) * time.Second
	}
	if m.MaxRetries != nil {
		o.MaxRetries = *m.MaxRetries
	}
	return o, nil
}
