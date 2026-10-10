//go:build live

package agent

import (
	"context"
	"image"
	"image/draw"
	"os"
	"testing"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/llm"
)

// TestE8_1_S3_LiveUITARSLocatesCartera hace UNA llamada real a
// bytedance/ui-tars-1.5-7b (OpenRouter) con la captura de inicio de SIGECOM
// (testdata/sigecom_home.png, 1280x800) y exige que el punto caiga en el menu
// "Cartera". Caja medida en esa captura: texto en x 181-226, y 44-52; el item
// del menu ocupa la barra y 34-60 entre "Comercial" (termina ~155) y
// "Herramientas" (empieza ~252). Caja aceptada: x 172-236, y 36-60.
func TestE8_1_S3_LiveUITARSLocatesCartera(t *testing.T) {
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
	ms, err := llm.ModelsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	o, err := ms.ClientOptions(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	o.MaxRetries = 0 // una sola llamada real
	img := loadFixture(t, "sigecom_home.png")
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	if b := rgba.Bounds(); b.Dx() != 1280 || b.Dy() != 800 {
		t.Fatalf("la captura mide %v", b)
	}
	model := "bytedance/ui-tars-1.5-7b"
	if v := os.Getenv("SO_E8_1_GROUNDER"); v != "" {
		model = v
	}
	rec := &recordingChatter{inner: llm.NewClient(o)}
	g := &UITARS{LLM: rec, Role: llm.Role{Model: model, MaxTokens: 64}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	x, y, u, err := g.Ground(ctx, rgba, "menú Cartera")
	t.Logf("modelo=%s salida=%q tokens_in=%d tokens_out=%d costo=%.6f reportado=%v punto=(%d,%d)",
		model, rec.content, u.PromptTokens, u.CompletionTokens, u.CostUSD, u.CostReported, x, y)
	if err != nil {
		t.Fatal(err)
	}
	box := image.Rect(172, 36, 237, 61)
	if !image.Pt(x, y).In(box) {
		t.Fatalf("punto (%d,%d) fuera de la caja del menu Cartera %v", x, y, box)
	}
}

// recordingChatter guarda la ultima salida cruda (para el log del test).
type recordingChatter struct {
	inner   llm.Chatter
	content string
}

func (r *recordingChatter) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := r.inner.Chat(ctx, req)
	r.content = resp.Content
	return resp, err
}
