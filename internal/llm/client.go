package llm

// Cliente de chat completions de OpenRouter (API compatible con OpenAI) con
// imagen, salida JSON, timeout por llamada, reintentos acotados y contabilidad
// de tokens y costo por tarea.
//
// Formato de usage verificado en https://openrouter.ai/docs/use-cases/usage-accounting
// (2026-10-10): la respuesta trae siempre usage.prompt_tokens,
// usage.completion_tokens, usage.total_tokens y usage.cost (en creditos de
// OpenRouter, 1 credito = 1 US$). Si cost viene, es el costo real cobrado; si
// no, se calcula con los precios configurados.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errores del cliente.
var (
	// ErrBudget: el costo acumulado de la tarea alcanzo budgets.max_cost_usd.
	ErrBudget = errors.New("presupuesto agotado")
	// ErrTimeout: la llamada no respondio dentro de su timeout.
	ErrTimeout = errors.New("llm: tiempo de espera agotado")
	// ErrStatus: el servidor respondio con un codigo de error no reintentable
	// o se agotaron los reintentos.
	ErrStatus = errors.New("llm: respuesta de error del proveedor")
)

// Valores por defecto.
const (
	DefaultCallTimeout = 45 * time.Second
	DefaultMaxRetries  = 2
	DefaultBackoff     = 500 * time.Millisecond
	maxBackoff         = 8 * time.Second
	maxErrBody         = 300
)

// Part es una parte del contenido de un mensaje: texto o imagen.
type Part struct {
	Text      string // si ImageMIME es "", es texto
	Image     []byte
	ImageMIME string
}

// TextPart crea una parte de texto.
func TextPart(s string) Part { return Part{Text: s} }

// ImagePart crea una parte de imagen.
func ImagePart(b []byte, mime string) Part { return Part{Image: b, ImageMIME: mime} }

// Message es un mensaje del chat.
type Message struct {
	Role  string // system, user, assistant
	Parts []Part
}

// Images cuenta las imagenes del mensaje.
func (m Message) Images() int {
	n := 0
	for _, p := range m.Parts {
		if p.ImageMIME != "" {
			n++
		}
	}
	return n
}

// Text concatena las partes de texto del mensaje.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.ImageMIME == "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Request es una llamada al modelo.
type Request struct {
	Model       string
	Messages    []Message
	Temperature float64
	MaxTokens   int
	JSON        bool // pide response_format json_object
	// ReasoningEffort se manda como reasoning.effort de OpenRouter ("none"
	// apaga el razonamiento; "" no manda nada y usa el default del modelo).
	ReasoningEffort string
	// Purpose describe la llamada en trazas y logs (clasificar, actuar, ...).
	Purpose string
}

// Usage son los tokens y el costo de una llamada.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	// CostReported indica que el costo vino en la respuesta (usage.cost).
	CostReported bool
	// ReasoningTokens es la parte de CompletionTokens usada en razonamiento.
	ReasoningTokens int
}

// Response es la respuesta del modelo.
type Response struct {
	Content      string
	Model        string
	Usage        Usage
	FinishReason string // stop, length, ...
}

// Chatter es lo que el agente necesita de un modelo. Lo implementan Client,
// Meter y los LLM falsos de los tests.
type Chatter interface {
	Chat(ctx context.Context, req Request) (Response, error)
}

// Price es el precio de un modelo en US$ por millon de tokens.
type Price struct {
	InPerM  float64
	OutPerM float64
}

// Cost calcula el costo de una llamada con estos precios.
func (p Price) Cost(promptTokens, completionTokens int) float64 {
	return float64(promptTokens)*p.InPerM/1e6 + float64(completionTokens)*p.OutPerM/1e6
}

// Options configura un Client.
type Options struct {
	BaseURL     string // https://openrouter.ai/api/v1
	APIKey      string // nunca aparece en errores
	HTTP        *http.Client
	CallTimeout time.Duration // por intento
	MaxRetries  int           // reintentos ante 429/5xx/red (no ante timeout de la tarea)
	Backoff     time.Duration // espera base; se duplica en cada reintento
	Prices      map[string]Price
	// Sleep permite a los tests observar las esperas. nil = espera real.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client llama a OpenRouter. Es seguro para uso concurrente.
type Client struct {
	o Options
}

// NewClient crea un Client con valores por defecto donde falten.
func NewClient(o Options) *Client {
	if o.HTTP == nil {
		o.HTTP = &http.Client{}
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = DefaultCallTimeout
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 0
	}
	if o.Backoff <= 0 {
		o.Backoff = DefaultBackoff
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	return &Client{o: o}
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

// wire types (OpenAI chat completions).
type wireContent struct {
	Type     string   `json:"type"`
	Text     *string  `json:"text,omitempty"`
	ImageURL *wireURL `json:"image_url,omitempty"`
}

type wireURL struct {
	URL string `json:"url"`
}

type wireMessage struct {
	Role    string        `json:"role"`
	Content []wireContent `json:"content"`
}

type wireRequest struct {
	Model          string            `json:"model"`
	Messages       []wireMessage     `json:"messages"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
	Reasoning      map[string]string `json:"reasoning,omitempty"`
}

type wireResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		Details          *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		Cost *float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (c *Client) body(req Request) ([]byte, error) {
	w := wireRequest{Model: req.Model, Temperature: req.Temperature, MaxTokens: req.MaxTokens}
	if req.JSON {
		w.ResponseFormat = map[string]string{"type": "json_object"}
	}
	if req.ReasoningEffort != "" {
		w.Reasoning = map[string]string{"effort": req.ReasoningEffort}
	}
	for _, m := range req.Messages {
		wm := wireMessage{Role: m.Role}
		for _, p := range m.Parts {
			if p.ImageMIME != "" {
				url := "data:" + p.ImageMIME + ";base64," + base64.StdEncoding.EncodeToString(p.Image)
				wm.Content = append(wm.Content, wireContent{Type: "image_url", ImageURL: &wireURL{URL: url}})
				continue
			}
			text := p.Text
			if strings.TrimSpace(text) == "" {
				text = "(vacio)" // OpenRouter rechaza partes de texto vacias (HTTP 400)
			}
			wm.Content = append(wm.Content, wireContent{Type: "text", Text: &text})
		}
		w.Messages = append(w.Messages, wm)
	}
	return json.Marshal(w)
}

// Chat hace la llamada con reintentos ante 429, 5xx y errores de red. Cada
// intento tiene su timeout (CallTimeout) y todos respetan ctx.
func (c *Client) Chat(ctx context.Context, req Request) (Response, error) {
	body, err := c.body(req)
	if err != nil {
		return Response{}, err
	}
	var last error
	for attempt := 0; attempt <= c.o.MaxRetries; attempt++ {
		if attempt > 0 {
			wait := c.o.Backoff << (attempt - 1)
			if wait > maxBackoff {
				wait = maxBackoff
			}
			if ra, ok := retryAfter(last); ok && ra > wait && ra <= maxBackoff {
				wait = ra
			}
			if err := c.o.Sleep(ctx, wait); err != nil {
				return Response{}, fmt.Errorf("llm: %w (tras %d intentos: %v)", err, attempt, last)
			}
		}
		resp, retry, err := c.once(ctx, req, body)
		if err == nil {
			return resp, nil
		}
		last = err
		if !retry || ctx.Err() != nil {
			return Response{}, err
		}
	}
	return Response{}, last
}

type statusError struct {
	code       int
	msg        string
	retryAfter time.Duration
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%v: HTTP %d: %s", ErrStatus, e.code, e.msg)
}

func (e *statusError) Unwrap() error { return ErrStatus }

func retryAfter(err error) (time.Duration, bool) {
	var se *statusError
	if errors.As(err, &se) && se.retryAfter > 0 {
		return se.retryAfter, true
	}
	return 0, false
}

func (c *Client) once(ctx context.Context, req Request, body []byte) (Response, bool, error) {
	cctx, cancel := context.WithTimeoutCause(ctx, c.o.CallTimeout, ErrTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(cctx, http.MethodPost, c.o.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, false, fmt.Errorf("llm: %s", c.scrub(err.Error()))
	}
	hr.Header.Set("Authorization", "Bearer "+c.o.APIKey)
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("X-Title", "screen-operator")
	res, err := c.o.HTTP.Do(hr)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			// El contexto de la tarea se cancelo o vencio: no se reintenta.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return Response{}, false, fmt.Errorf("%w: %v", ErrTimeout, context.Cause(ctx))
			}
			return Response{}, false, fmt.Errorf("llm: %w", context.Cause(ctx))
		case errors.Is(context.Cause(cctx), ErrTimeout):
			return Response{}, true, fmt.Errorf("%w (%s)", ErrTimeout, c.o.CallTimeout)
		}
		return Response{}, true, fmt.Errorf("llm: error de red: %s", c.scrub(rootCause(err)))
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		if errors.Is(context.Cause(cctx), ErrTimeout) && ctx.Err() == nil {
			return Response{}, true, fmt.Errorf("%w (%s)", ErrTimeout, c.o.CallTimeout)
		}
		if ctx.Err() != nil {
			return Response{}, false, fmt.Errorf("%w: %v", ErrTimeout, context.Cause(ctx))
		}
		return Response{}, true, fmt.Errorf("llm: lectura de la respuesta: %s", c.scrub(rootCause(err)))
	}
	if res.StatusCode != http.StatusOK {
		se := &statusError{code: res.StatusCode, msg: c.scrub(errMessage(raw))}
		if s := res.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				se.retryAfter = time.Duration(n) * time.Second
			}
		}
		retry := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		return Response{}, retry, se
	}
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return Response{}, true, fmt.Errorf("llm: respuesta no es JSON: %v", err)
	}
	if w.Error != nil {
		return Response{}, true, &statusError{code: res.StatusCode, msg: c.scrub(w.Error.Message)}
	}
	if len(w.Choices) == 0 {
		return Response{}, true, fmt.Errorf("%w: respuesta sin choices", ErrStatus)
	}
	out := Response{Model: w.Model, Content: contentText(w.Choices[0].Message.Content), FinishReason: w.Choices[0].FinishReason}
	if w.Usage != nil {
		out.Usage.PromptTokens = w.Usage.PromptTokens
		out.Usage.CompletionTokens = w.Usage.CompletionTokens
		if w.Usage.Details != nil {
			out.Usage.ReasoningTokens = w.Usage.Details.ReasoningTokens
		}
		if w.Usage.Cost != nil {
			out.Usage.CostUSD = *w.Usage.Cost
			out.Usage.CostReported = true
		}
	}
	if !out.Usage.CostReported {
		out.Usage.CostUSD = c.o.Prices[req.Model].Cost(out.Usage.PromptTokens, out.Usage.CompletionTokens)
	}
	return out, false, nil
}

// contentText acepta content como string o como lista de partes de texto.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return string(raw)
}

func errMessage(raw []byte) string {
	var w wireResponse
	if json.Unmarshal(raw, &w) == nil && w.Error != nil && w.Error.Message != "" {
		return truncate(w.Error.Message)
	}
	return truncate(string(raw))
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxErrBody {
		return s[:maxErrBody] + "..."
	}
	return s
}

// StripFences quita cercas de markdown (```json ... ```) y texto alrededor del
// primer objeto JSON. Algunos modelos las agregan aunque se pida JSON.
func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "{") {
		i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
		if i >= 0 && j > i {
			s = s[i : j+1]
		}
	}
	return s
}

// rootCause reduce un error de net/http a su causa, sin la URL.
func rootCause(err error) string {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err.Error()
		}
		err = u
	}
}

// scrub quita la API key de un texto de terceros.
func (c *Client) scrub(s string) string {
	if c.o.APIKey != "" {
		s = strings.ReplaceAll(s, c.o.APIKey, "[redactado]")
	}
	return s
}

// Meter envuelve un Chatter con el presupuesto de costo de una tarea: rechaza
// una llamada cuando el acumulado ya alcanzo el maximo, sin llamar al
// servidor, y acumula tokens y costo de cada respuesta.
type Meter struct {
	inner Chatter
	max   float64
	mu    sync.Mutex
	in    int
	out   int
	cost  float64
	calls int
}

// NewMeter crea el medidor de una tarea. maxUSD <= 0 = sin tope.
func NewMeter(inner Chatter, maxUSD float64) *Meter {
	return &Meter{inner: inner, max: maxUSD}
}

// Chat implementa Chatter con control de presupuesto.
func (m *Meter) Chat(ctx context.Context, req Request) (Response, error) {
	m.mu.Lock()
	spent := m.cost
	m.mu.Unlock()
	if m.max > 0 && spent >= m.max-1e-12 {
		return Response{}, fmt.Errorf("%w: %.5f US$ de %.2f US$", ErrBudget, spent, m.max)
	}
	resp, err := m.inner.Chat(ctx, req)
	if err != nil {
		return resp, err
	}
	m.mu.Lock()
	m.in += resp.Usage.PromptTokens
	m.out += resp.Usage.CompletionTokens
	m.cost += resp.Usage.CostUSD
	m.calls++
	m.mu.Unlock()
	return resp, nil
}

// Add suma un costo ya incurrido (p. ej. al iniciar con un acumulado).
func (m *Meter) Add(u Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.in += u.PromptTokens
	m.out += u.CompletionTokens
	m.cost += u.CostUSD
}

// Totals devuelve tokens de entrada, de salida, costo y llamadas acumulados.
func (m *Meter) Totals() (in, out int, cost float64, calls int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.in, m.out, m.cost, m.calls
}

// CostUSD es el costo acumulado.
func (m *Meter) CostUSD() float64 {
	_, _, c, _ := m.Totals()
	return c
}
