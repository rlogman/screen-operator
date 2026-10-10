package agent

// Grounder opcional (historia 8.1). Con models.roles.grounder configurado, el
// actor describe el elemento objetivo de un clic ("elemento") y el grounder
// fija el punto; sin el rol se usan las coordenadas del actor, como antes.
//
// Formato de UI-TARS-1.5 (verificado en github.com/bytedance/UI-TARS:
// README_coordinates.md y codes/ui_tars/action_parser.py, rama main): la
// accion trae el punto como click(point='<point>x y</point>') o
// click(start_box='(x,y)'), en PIXELES ABSOLUTOS de la imagen redimensionada
// por smart_resize (lados multiplos de 28, entre 100*28*28 y 16384*28*28
// pixeles); a pantalla: x/ancho_redimensionado*ancho_original. Para que el
// proveedor no vuelva a redimensionar, la imagen se envia ya con el tamano de
// smart_resize y el punto vuelve con esa misma escala.

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"regexp"
	"strconv"

	"screenoperator/internal/llm"
	"screenoperator/internal/screen"
)

// Grounder ubica un elemento descrito en img y devuelve el punto en
// coordenadas de img.
type Grounder interface {
	Ground(ctx context.Context, img *image.RGBA, desc string) (x, y int, u llm.Usage, err error)
}

func (a *Agent) groundingOn() bool { return a.Cfg.Grounder.Model != "" }

// groundable: acciones cuyo punto puede fijar el grounder.
func groundable(k Kind) bool {
	return k == KindClick || k == KindDoubleClick || k == KindHover || k == KindCommit
}

// ground reemplaza X,Y de act por el punto del grounder. Si el grounder falla
// o devuelve un punto fuera de la imagen, quedan las coordenadas del actor.
func (a *Agent) ground(ctx context.Context, act *Action) (llm.Usage, error) {
	if !a.groundingOn() || a.Locator == nil || !groundable(act.Kind) || act.Element == "" || a.view == nil {
		return llm.Usage{}, nil
	}
	v := *a.view
	x, y, u, err := a.Locator.Ground(ctx, v.Img, act.Element)
	if err != nil {
		a.logInfo("grounder sin punto; se usan las coordenadas del actor", "elemento", act.Element, "error", errText(err))
		return u, err
	}
	w, h := v.Size()
	if x < 0 || x >= w || y < 0 || y >= h {
		a.logInfo("grounder fuera de la imagen; se usan las coordenadas del actor", "elemento", act.Element, "x", x, "y", y)
		return u, nil
	}
	sx, sy := v.ToScreen(x, y)
	a.logInfo("punto del grounder", "elemento", act.Element, "actor_x", act.X, "actor_y", act.Y, "x", sx, "y", sy)
	act.ImgX, act.ImgY = x, y
	act.X, act.Y = sx, sy
	act.Grounded = true
	return u, nil
}

// UITARS es el Grounder sobre bytedance/ui-tars-1.5-7b (o compatible) via LLM.
type UITARS struct {
	LLM  llm.Chatter
	Role llm.Role
}

// Constantes de smart_resize de UI-TARS-1.5 (action_parser.py).
const (
	uitarsFactor    = 28
	uitarsMinPixels = 100 * 28 * 28
	uitarsMaxPixels = 16384 * 28 * 28
)

// SmartResize replica smart_resize de UI-TARS-1.5: devuelve (alto, ancho).
func SmartResize(h, w int) (int, int, error) {
	if h <= 0 || w <= 0 {
		return 0, 0, fmt.Errorf("tamano invalido %dx%d", w, h)
	}
	if float64(max(h, w))/float64(min(h, w)) > 200 {
		return 0, 0, fmt.Errorf("proporcion %dx%d mayor que 200", w, h)
	}
	f := float64(uitarsFactor)
	hb := max(uitarsFactor, int(math.RoundToEven(float64(h)/f))*uitarsFactor)
	wb := max(uitarsFactor, int(math.RoundToEven(float64(w)/f))*uitarsFactor)
	if hb*wb > uitarsMaxPixels {
		beta := math.Sqrt(float64(h*w) / uitarsMaxPixels)
		hb = int(math.Floor(float64(h)/beta/f)) * uitarsFactor
		wb = int(math.Floor(float64(w)/beta/f)) * uitarsFactor
	} else if hb*wb < uitarsMinPixels {
		beta := math.Sqrt(uitarsMinPixels / float64(h*w))
		hb = int(math.Ceil(float64(h)*beta/f)) * uitarsFactor
		wb = int(math.Ceil(float64(w)*beta/f)) * uitarsFactor
	}
	return hb, wb, nil
}

// Ground envia img (redimensionada con smart_resize) y la descripcion, y
// convierte el punto devuelto a coordenadas de img.
func (g *UITARS) Ground(ctx context.Context, img *image.RGBA, desc string) (int, int, llm.Usage, error) {
	b := img.Bounds()
	rh, rw, err := SmartResize(b.Dy(), b.Dx())
	if err != nil {
		return 0, 0, llm.Usage{}, err
	}
	sent := img
	if rw != b.Dx() || rh != b.Dy() {
		sent = screen.Resize(img, rw, rh)
	}
	png, err := screen.EncodePNG(sent)
	if err != nil {
		return 0, 0, llm.Usage{}, err
	}
	maxTok := g.Role.MaxTokens
	if maxTok <= 0 {
		maxTok = 64
	}
	resp, err := g.LLM.Chat(ctx, llm.Request{Model: g.Role.Model, Temperature: g.Role.Temperature, MaxTokens: maxTok,
		Purpose: "ubicar", ReasoningEffort: g.Role.Reasoning, Messages: []llm.Message{
			{Role: "user", Parts: []llm.Part{llm.TextPart(render("grounder_task", desc)), llm.ImagePart(png, "image/png")}},
		}})
	if err != nil {
		return 0, 0, resp.Usage, err
	}
	px, py, err := ParseUITARSPoint(resp.Content)
	if err != nil {
		return 0, 0, resp.Usage, err
	}
	x := int(float64(px) / float64(rw) * float64(b.Dx()))
	y := int(float64(py) / float64(rh) * float64(b.Dy()))
	return b.Min.X + x, b.Min.Y + y, resp.Usage, nil
}

var (
	reUITARSPoint = regexp.MustCompile(`<point>\s*(\d+(?:\.\d+)?)[\s,]+(\d+(?:\.\d+)?)\s*</point>`)
	// (x,y) o (x1,y1,x2,y2), con o sin <|box_start|>; start_box / point.
	reUITARSBox = regexp.MustCompile(`\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)\s*(?:,\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)\s*)?\)`)
)

// ErrNoPoint: la salida del grounder no trae un punto reconocible.
var ErrNoPoint = errors.New("la salida del grounder no trae un punto")

// ParseUITARSPoint extrae el punto de la accion de UI-TARS en la escala de la
// imagen enviada: <point>x y</point>, start_box='(x,y)' o una caja
// (x1,y1,x2,y2), de la que se toma el centro.
func ParseUITARSPoint(s string) (int, int, error) {
	if m := reUITARSPoint.FindStringSubmatch(s); m != nil {
		return num(m[1]), num(m[2]), nil
	}
	if m := reUITARSBox.FindStringSubmatch(s); m != nil {
		x, y := num(m[1]), num(m[2])
		if m[3] != "" {
			x, y = (x+num(m[3]))/2, (y+num(m[4]))/2
		}
		return x, y, nil
	}
	return 0, 0, fmt.Errorf("%w: %q", ErrNoPoint, truncate(s, 200))
}

func num(s string) int {
	f, _ := strconv.ParseFloat(s, 64)
	return int(math.Round(f))
}
