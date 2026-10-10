package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody limita lo que el harness lee de una respuesta.
const maxBody = 1 << 20

// CheckItem es un chequeo de POST /__check.
type CheckItem struct {
	Nombre string `json:"nombre"`
	OK     bool   `json:"ok"`
}

// CheckResult es la respuesta de POST /__check.
type CheckResult struct {
	Pass   bool        `json:"pass"`
	Checks []CheckItem `json:"checks"`
}

// ErrCheckUnsupported indica que /__check no acepto la prueba (respuesta 4xx
// distinta de 401/403, o un cuerpo sin la forma {pass, checks}).
var ErrCheckUnsupported = errors.New("/__check no acepta la prueba")

// Desktop habla con /__reset y /__check del escritorio.
type Desktop struct {
	BaseURL string // https://host (sin /websockify)
	Token   string
	Client  *http.Client
}

// DesktopBase deriva la base HTTP del escritorio y la URL del websocket a
// partir de cualquiera de las dos formas (wss://host/websockify o https://host).
func DesktopBase(raw string) (base, ws string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", errors.New("URL de escritorio invalida")
	}
	path := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/websockify")
	var httpScheme, wsScheme string
	switch strings.ToLower(u.Scheme) {
	case "wss", "https":
		httpScheme, wsScheme = "https", "wss"
	case "ws", "http":
		httpScheme, wsScheme = "http", "ws"
	default:
		return "", "", fmt.Errorf("esquema de escritorio %q no soportado", u.Scheme)
	}
	base = (&url.URL{Scheme: httpScheme, Host: u.Host, Path: path}).String()
	ws = (&url.URL{Scheme: wsScheme, Host: u.Host, Path: path + "/websockify"}).String()
	return base, ws, nil
}

func (d *Desktop) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Reset llama POST /__reset.
func (d *Desktop) Reset(ctx context.Context) error {
	code, body, err := do(ctx, d.client(), http.MethodPost, d.BaseURL+"/__reset", d.Token, nil)
	if err != nil {
		return fmt.Errorf("/__reset: %w", err)
	}
	if code/100 != 2 {
		return fmt.Errorf("/__reset respondio %d: %s", code, snippet(body))
	}
	return nil
}

// Check manda el JSON de la prueba a POST /__check.
func (d *Desktop) Check(ctx context.Context, raw []byte) (*CheckResult, error) {
	code, body, err := do(ctx, d.client(), http.MethodPost, d.BaseURL+"/__check", d.Token, raw)
	if err != nil {
		return nil, fmt.Errorf("/__check: %w", err)
	}
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return nil, fmt.Errorf("/__check respondio %d (token del escritorio)", code)
	case code/100 == 4:
		return nil, fmt.Errorf("%w: %d %s", ErrCheckUnsupported, code, snippet(body))
	case code/100 != 2:
		return nil, fmt.Errorf("/__check respondio %d: %s", code, snippet(body))
	}
	var raw2 struct {
		Pass   *bool       `json:"pass"`
		Checks []CheckItem `json:"checks"`
	}
	if err := json.Unmarshal(body, &raw2); err != nil || raw2.Pass == nil {
		return nil, fmt.Errorf("%w: respuesta sin {pass, checks}: %s", ErrCheckUnsupported, snippet(body))
	}
	return &CheckResult{Pass: *raw2.Pass, Checks: raw2.Checks}, nil
}

// Agent habla con el contrato del agente.
type Agent struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func (a *Agent) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Submit llama POST /tasks y devuelve el task_id.
func (a *Agent) Submit(ctx context.Context, instruction, desktopURL, password string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"tarea":      instruction,
		"escritorio": map[string]string{"url": desktopURL, "password": password},
	})
	code, resp, err := do(ctx, a.client(), http.MethodPost, a.BaseURL+"/tasks", a.Token, body)
	if err != nil {
		return "", fmt.Errorf("POST /tasks: %w", err)
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("POST /tasks respondio %d: %s", code, snippet(resp))
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if err := json.Unmarshal(resp, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("POST /tasks sin task_id: %s", snippet(resp))
	}
	return out.ID, nil
}

// TaskState es la respuesta de GET /tasks/{id}.
type TaskState struct {
	Estado  string `json:"estado"`
	Resumen string `json:"resumen"`
}

// Final dice si el estado es final.
func (s TaskState) Final() bool { return s.Estado == "terminada" || s.Estado == "fallida" }

// Get llama GET /tasks/{id}.
func (a *Agent) Get(ctx context.Context, id string) (TaskState, error) {
	code, resp, err := do(ctx, a.client(), http.MethodGet, a.BaseURL+"/tasks/"+url.PathEscape(id), a.Token, nil)
	if err != nil {
		return TaskState{}, fmt.Errorf("GET /tasks: %w", err)
	}
	var st TaskState
	if err := json.Unmarshal(resp, &st); err != nil {
		return TaskState{}, fmt.Errorf("GET /tasks respondio %d sin JSON: %s", code, snippet(resp))
	}
	if code != http.StatusOK && !st.Final() {
		return TaskState{}, fmt.Errorf("GET /tasks respondio %d: %s", code, snippet(resp))
	}
	return st, nil
}

// Metrics es lo que expone GET /tasks/{id}/metrics (diagnostico del agente).
type Metrics struct {
	Pasos         int     `json:"pasos"`
	CostoUSD      float64 `json:"costo_usd"`
	TokensEntrada int     `json:"tokens_entrada"`
	TokensSalida  int     `json:"tokens_salida"`
}

// Metrics llama GET /tasks/{id}/metrics.
func (a *Agent) Metrics(ctx context.Context, id string) (*Metrics, error) {
	code, resp, err := do(ctx, a.client(), http.MethodGet, a.BaseURL+"/tasks/"+url.PathEscape(id)+"/metrics", a.Token, nil)
	if err != nil {
		return nil, fmt.Errorf("GET /tasks/{id}/metrics: %w", err)
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /tasks/{id}/metrics respondio %d", code)
	}
	var m Metrics
	if err := json.Unmarshal(resp, &m); err != nil {
		return nil, fmt.Errorf("GET /tasks/{id}/metrics sin JSON: %s", snippet(resp))
	}
	return &m, nil
}

func do(ctx context.Context, c *http.Client, method, u, token string, body []byte) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return 0, nil, errors.New("URL invalida")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		// El error de net/http trae la URL completa; se resume para no
		// filtrar nada que venga en ella.
		var ue *url.Error
		if errors.As(err, &ue) {
			return 0, nil, ue.Err
		}
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode, b, err
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
