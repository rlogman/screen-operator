package llm

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// roundTrip atiende las peticiones en memoria (el sandbox no deja bindear).
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body))}
}

const okBody = `{"model":"m","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":1000,"completion_tokens":100}}`

func TestE6_1_S1_CostFromConfiguredPrices(t *testing.T) {
	var gotAuth, gotBody string
	c := NewClient(Options{
		BaseURL: "https://openrouter.test/api/v1",
		APIKey:  "sk-test",
		HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("Authorization")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			return jsonResp(200, okBody), nil
		})},
		Prices: map[string]Price{"m": {InPerM: 0.15, OutPerM: 0.60}},
	})
	m := NewMeter(c, 0.30)
	resp, err := m.Chat(context.Background(), Request{Model: "m", JSON: true, Messages: []Message{
		{Role: "user", Parts: []Part{TextPart("hola"), ImagePart([]byte{1, 2, 3}, "image/jpeg")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != `{"ok":true}` {
		t.Fatalf("content = %q", resp.Content)
	}
	if got := m.CostUSD(); math.Abs(got-0.00021) > 1e-12 {
		t.Fatalf("costo acumulado = %v, se esperaba 0.00021", got)
	}
	in, out, _, calls := m.Totals()
	if in != 1000 || out != 100 || calls != 1 {
		t.Fatalf("tokens = %d/%d llamadas %d", in, out, calls)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	for _, want := range []string{`"response_format":{"type":"json_object"}`, `"type":"image_url"`, `data:image/jpeg;base64,AQID`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("el cuerpo no contiene %s: %s", want, gotBody)
		}
	}
}

func TestE6_1_S1_ReportedCostWins(t *testing.T) {
	// Si OpenRouter trae usage.cost, ese es el costo (lo cobrado de verdad).
	c := NewClient(Options{BaseURL: "https://x/api/v1", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":1000,"completion_tokens":100,"cost":0.0005}}`), nil
	})}, Prices: map[string]Price{"m": {0.15, 0.60}}})
	resp, err := c.Chat(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Usage.CostReported || resp.Usage.CostUSD != 0.0005 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestE6_1_S2_Retries429WithGrowingBackoff(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	var waits []time.Duration
	c := NewClient(Options{
		BaseURL: "https://x/api/v1",
		HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls <= 2 {
				return jsonResp(429, `{"error":{"message":"rate limited"}}`), nil
			}
			return jsonResp(200, okBody), nil
		})},
		MaxRetries: 2,
		Backoff:    100 * time.Millisecond,
		Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
	})
	resp, err := c.Chat(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("tras dos 429 debia responder: %v", err)
	}
	if calls != 3 {
		t.Fatalf("intentos = %d, se esperaban 3", calls)
	}
	if len(waits) != 2 || !(waits[1] > waits[0]) {
		t.Fatalf("esperas = %v, se esperaban 2 crecientes", waits)
	}
	if resp.Usage.PromptTokens != 1000 {
		t.Fatalf("usage = %+v", resp.Usage)
	}

	// Con un solo reintento permitido, el segundo 429 es el error final.
	calls = 0
	c.o.MaxRetries = 1
	if _, err := c.Chat(context.Background(), Request{Model: "m"}); !errors.Is(err, ErrStatus) || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v", err)
	}
}

func TestE6_1_S3_TimeoutRespectsTaskContext(t *testing.T) {
	hang := roundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	// (a) Timeout de la llamada: error de timeout (sin reintentos para acotar).
	c := NewClient(Options{BaseURL: "https://x/api/v1", APIKey: "sk-secreta", HTTP: &http.Client{Transport: hang},
		CallTimeout: 50 * time.Millisecond, MaxRetries: -1})
	start := time.Now()
	_, err := c.Chat(context.Background(), Request{Model: "m"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, se esperaba ErrTimeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("tardo %v", d)
	}
	if strings.Contains(err.Error(), "sk-secreta") || strings.Contains(err.Error(), "https://x") {
		t.Fatalf("el error filtra la key o la URL: %v", err)
	}
	// (b) El contexto de la tarea vence antes que el timeout de la llamada: se
	// respeta y no se reintenta.
	c = NewClient(Options{BaseURL: "https://x/api/v1", HTTP: &http.Client{Transport: hang},
		CallTimeout: time.Minute, MaxRetries: 3})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start = time.Now()
	_, err = c.Chat(ctx, Request{Model: "m"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, se esperaba ErrTimeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("no respeto el contexto de la tarea: %v", d)
	}
}

// OpenRouter responde 400 a una parte de texto sin "text" (visto en vivo cuando
// el modelo devolvio contenido vacio y se reenvio en la reparacion).
func TestEmptyTextPartStillCarriesText(t *testing.T) {
	var body string
	c := NewClient(Options{BaseURL: "https://x/api/v1", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		return jsonResp(200, okBody), nil
	})}})
	_, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "assistant", Parts: []Part{TextPart("")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `{"type":"text","text":"(vacio)"}`) {
		t.Fatalf("cuerpo = %s", body)
	}
}

func TestE6_1_S4_BudgetExhaustedRejectsWithoutCalling(t *testing.T) {
	calls := 0
	c := NewClient(Options{BaseURL: "https://x/api/v1", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResp(200, okBody), nil
	})}})
	m := NewMeter(c, 0.30)
	m.Add(Usage{CostUSD: 0.30})
	_, err := m.Chat(context.Background(), Request{Model: "m"})
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "presupuesto agotado") {
		t.Fatalf("err = %v", err)
	}
	if calls != 0 {
		t.Fatalf("llamo al servidor %d veces", calls)
	}
}
