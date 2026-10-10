//go:build live

package llm

import (
	"context"
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/screen"
)

// TestE6_1_S5_LiveMultimodalJSON envia una captura real de 1280x800 a OpenRouter
// y exige JSON parseable. Modelo: models.roles.actor de base.yaml, o la lista
// separada por comas en SO_E6_1_MODELS (para comparar candidatos).
func TestE6_1_S5_LiveMultimodalJSON(t *testing.T) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("falta OPENROUTER_API_KEY (build tag live)")
	}
	l, err := config.NewLoader("../../config", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := l.ForTask("wss://x/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := ModelsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	o, err := ms.ClientOptions(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(o)
	f, err := os.Open("../agent/testdata/sigecom_home.png")
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1280 || b.Dy() != 800 {
		t.Fatalf("la captura mide %v", b)
	}
	jpg, err := screen.EncodeJPEG(img, 80)
	if err != nil {
		t.Fatal(err)
	}
	models := []string{ms.Role("actor").Model}
	if v := os.Getenv("SO_E6_1_MODELS"); v != "" {
		models = strings.Split(v, ",")
	}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			resp, err := c.Chat(ctx, Request{Model: model, JSON: true, MaxTokens: 300, Messages: []Message{
				{Role: "system", Parts: []Part{TextPart("Responde solo con un objeto JSON.")}},
				{Role: "user", Parts: []Part{TextPart(`Describe la captura en JSON: {"titulo": "...", "fecha_visible": "DD/MM/AAAA o null", "menus": ["..."]}`),
					ImagePart(jpg, "image/jpeg")}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			var v map[string]any
			if err := json.Unmarshal([]byte(StripFences(resp.Content)), &v); err != nil {
				t.Fatalf("no es JSON parseable: %v\n%s", err, resp.Content)
			}
			t.Logf("modelo=%s tokens_in=%d tokens_out=%d costo=%.6f reportado=%v json=%v",
				resp.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.CostUSD, resp.Usage.CostReported, v)
		})
	}
}
