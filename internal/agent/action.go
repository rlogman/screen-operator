package agent

// Esquema de acciones del modelo y parser estricto. El modelo responde un
// objeto JSON por paso; las coordenadas son de la imagen que vio (captura
// reducida o zoom) y el parser las convierte a pantalla con la View del paso.

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"sort"
	"strings"

	"screenoperator/internal/llm"
	"screenoperator/internal/screen"
)

// Kind es el tipo de accion.
type Kind string

// Acciones del diseño (seccion "El loop del agente").
const (
	KindClick       Kind = "click"
	KindDoubleClick Kind = "double_click"
	KindHover       Kind = "hover"
	KindType        Kind = "type"
	KindKey         Kind = "key"
	KindScroll      Kind = "scroll"
	KindZoom        Kind = "zoom"
	KindExtract     Kind = "extract"
	KindCommit      Kind = "commit"
	KindFinish      Kind = "finish"
)

// Kinds lista las acciones validas, en el orden del prompt.
var Kinds = []Kind{KindClick, KindDoubleClick, KindHover, KindType, KindKey, KindScroll, KindZoom, KindExtract, KindCommit, KindFinish}

// Phase es una fase del loop.
type Phase string

// Fases del loop, en orden.
const (
	PhaseClassify Phase = "clasificar"
	PhaseOrient   Phase = "orientar"
	PhaseLocate   Phase = "localizar"
	PhaseAct      Phase = "actuar"
	PhaseVerify   Phase = "verificar"
	PhaseReport   Phase = "reportar"
)

var phaseOrder = map[Phase]int{PhaseClassify: 0, PhaseOrient: 1, PhaseLocate: 2, PhaseAct: 3, PhaseVerify: 4, PhaseReport: 5}

// Before indica si p va antes que q.
func (p Phase) Before(q Phase) bool { return phaseOrder[p] < phaseOrder[q] }

// Estados que el modelo puede pedir en finish.
const (
	StatusDone   = "terminada"
	StatusFailed = "fallida"
)

// View es la imagen que vio el modelo en un paso y como volver a pantalla:
// pantalla = Origin + imagen * Scale.
type View struct {
	Img     *image.RGBA
	Origin  image.Point
	ScaleX  float64
	ScaleY  float64
	ScreenW int
	ScreenH int
	Zoomed  bool
}

// FullView arma la View de una captura reducida para el modelo.
func FullView(s screen.ModelShot) View {
	b := s.Image.Bounds()
	return View{Img: s.Image, ScaleX: float64(s.ScreenW) / float64(b.Dx()), ScaleY: float64(s.ScreenH) / float64(b.Dy()),
		ScreenW: s.ScreenW, ScreenH: s.ScreenH}
}

// ZoomView arma la View de un recorte r (pantalla) ampliado en img.
func ZoomView(img *image.RGBA, r image.Rectangle, screenW, screenH int) View {
	b := img.Bounds()
	return View{Img: img, Origin: r.Min, ScaleX: float64(r.Dx()) / float64(b.Dx()), ScaleY: float64(r.Dy()) / float64(b.Dy()),
		ScreenW: screenW, ScreenH: screenH, Zoomed: true}
}

// Size es el tamano de la imagen del modelo.
func (v View) Size() (int, int) { return v.Img.Bounds().Dx(), v.Img.Bounds().Dy() }

// ToScreen convierte un punto de la imagen a pantalla, recortado a la pantalla.
func (v View) ToScreen(x, y int) (int, int) {
	sx := v.Origin.X + int(math.Round(float64(x)*v.ScaleX))
	sy := v.Origin.Y + int(math.Round(float64(y)*v.ScaleY))
	return clampInt(sx, 0, v.ScreenW-1), clampInt(sy, 0, v.ScreenH-1)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Action es una accion ya validada, en coordenadas de pantalla.
type Action struct {
	Kind   Kind
	Phase  Phase // fase que el modelo declara (localizar, actuar, verificar)
	Reason string
	Note   string // dato que el modelo quiere recordar (notas acumuladas)

	X, Y   int // pantalla (click, double_click, hover, type, scroll, commit)
	ImgX   int // imagen, tal como la dio el modelo
	ImgY   int
	HasXY  bool
	Region image.Rectangle // zoom, en pantalla

	Text    string // type
	Replace bool   // type: reemplazar el contenido del campo
	Combo   string // key
	Dir     string // scroll
	Amount  int    // scroll

	// extract
	What    string
	KeyCol  string
	Filters map[string]string
	Purpose string // conteo, suma, candidatos, datos

	// commit
	Target        string
	ConfirmDialog bool // confirma el dialogo que abrio un commit anterior sobre el mismo objetivo

	// Label es el texto del elemento bajo un click, leido con zoom antes de
	// consultar al guardrail (epica 7.3-S4). "" = no se leyo.
	Label string

	// finish (solicitud: el estado final lo fija el loop tras verificar)
	Finish *FinishRequest
}

// FinishRequest es lo que pide el modelo al cerrar. No fija el estado final:
// el loop verifica y los guardrails deciden.
type FinishRequest struct {
	Status      string   // terminada o fallida (pedido)
	Data        []string // datos de la respuesta, en el orden de la pregunta
	Reason      string
	Identifiers []string // identificadores que explican una falla (cedulas, NIT, codigos)
	Aggregate   string   // "", "conteo" o "suma:<columna>": el dato lo calcula el codigo
	Entity      string   // entidad tocada (cliente, pedido, ...)
	EntityID    string   // identificador del registro tocado
	Changed     []string // campos cambiados "campo = valor"
	Verified    bool     // el modelo dice haber releido el resultado
	Evidence    string   // lo que leyo al verificar
}

// String describe la accion en una linea para el historial (sin coordenadas de imagen).
func (a Action) String() string {
	switch a.Kind {
	case KindClick, KindDoubleClick, KindHover:
		return fmt.Sprintf("%s(%d,%d)", a.Kind, a.X, a.Y)
	case KindType:
		if a.HasXY {
			return fmt.Sprintf("type(%d,%d, %q, replace=%v)", a.X, a.Y, a.Text, a.Replace)
		}
		return fmt.Sprintf("type(%q)", a.Text)
	case KindKey:
		return fmt.Sprintf("key(%s)", a.Combo)
	case KindScroll:
		return fmt.Sprintf("scroll(%d,%d, %s, %d)", a.X, a.Y, a.Dir, a.Amount)
	case KindZoom:
		r := a.Region
		return fmt.Sprintf("zoom(%d,%d,%d,%d)", r.Min.X, r.Min.Y, r.Dx(), r.Dy())
	case KindExtract:
		return fmt.Sprintf("extract(%q, columna_id=%s)", a.What, a.KeyCol)
	case KindCommit:
		return fmt.Sprintf("commit(%d,%d, %q, objetivo=%s)", a.X, a.Y, a.What, a.Target)
	case KindFinish:
		if a.Finish != nil {
			return fmt.Sprintf("finish(%s, datos=%v)", a.Finish.Status, a.Finish.Data)
		}
	}
	return string(a.Kind)
}

// ParseError es una salida del modelo que no cumple el esquema. Field nombra
// el campo culpable ("" si la salida no es JSON).
type ParseError struct {
	Field string
	Msg   string
}

func (e *ParseError) Error() string {
	if e.Field == "" {
		return "salida invalida: " + e.Msg
	}
	return fmt.Sprintf("campo %q: %s", e.Field, e.Msg)
}

func perr(field, format string, a ...any) error {
	return &ParseError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// rawAction es el JSON del modelo. Los numeros se aceptan como float y se
// redondean (algunos modelos devuelven 120.0).
type rawAction struct {
	Action      string            `json:"action"`
	Fase        string            `json:"fase"`
	Reason      string            `json:"reason"`
	Razon       string            `json:"razon"`
	Nota        string            `json:"nota"`
	X           *float64          `json:"x"`
	Y           *float64          `json:"y"`
	W           *float64          `json:"w"`
	H           *float64          `json:"h"`
	Text        *string           `json:"text"`
	Replace     *bool             `json:"replace"`
	Combo       string            `json:"combo"`
	Direction   string            `json:"direction"`
	Amount      *float64          `json:"amount"`
	What        string            `json:"what"`
	Key         string            `json:"key"`
	Filters     map[string]any    `json:"filters"`
	Purpose     string            `json:"purpose"`
	Target      string            `json:"target"`
	Estado      string            `json:"estado"`
	Datos       json.RawMessage   `json:"datos"`
	Ident       []json.RawMessage `json:"identificadores"`
	Agregado    string            `json:"agregado"`
	Entidad     string            `json:"entidad"`
	EntidadID   string            `json:"id_registro"`
	Cambios     []string          `json:"cambios"`
	Verificado  *bool             `json:"verificado"`
	Evidencia   string            `json:"evidencia"`
	Dialogo     *bool             `json:"dialogo"`
	unknownKeys []string
}

var knownKeys = map[string]bool{
	"action": true, "fase": true, "reason": true, "razon": true, "nota": true, "x": true, "y": true, "w": true, "h": true,
	"text": true, "replace": true, "combo": true, "direction": true, "amount": true, "what": true, "key": true,
	"filters": true, "purpose": true, "target": true, "estado": true, "datos": true, "identificadores": true,
	"agregado": true, "entidad": true, "id_registro": true, "cambios": true, "verificado": true, "evidencia": true,
	"dialogo": true,
}

// Parse valida la salida del modelo contra el esquema y convierte las
// coordenadas de imagen a pantalla con v. Un error es *ParseError.
func Parse(content string, v View) (Action, error) {
	body := llm.StripFences(content)
	var r rawAction
	dec := json.NewDecoder(strings.NewReader(body))
	if err := dec.Decode(&r); err != nil {
		return Action{}, &ParseError{Msg: "no es un objeto JSON: " + shorten(err.Error())}
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &keys); err == nil {
		for k := range keys {
			if !knownKeys[k] {
				r.unknownKeys = append(r.unknownKeys, k)
			}
		}
		sort.Strings(r.unknownKeys)
	}
	if len(r.unknownKeys) > 0 {
		return Action{}, perr(r.unknownKeys[0], "campo desconocido")
	}
	a := Action{Kind: Kind(strings.ToLower(strings.TrimSpace(r.Action))), Reason: firstNonEmpty(r.Reason, r.Razon), Note: strings.TrimSpace(r.Nota)}
	if a.Kind == "" {
		return Action{}, perr("action", "falta la accion")
	}
	if !validKind(a.Kind) {
		return Action{}, perr("action", "accion %q desconocida; validas: %s", r.Action, kindList())
	}
	if r.Fase != "" {
		p := Phase(strings.ToLower(strings.TrimSpace(r.Fase)))
		if p != PhaseLocate && p != PhaseAct && p != PhaseVerify {
			return Action{}, perr("fase", "fase %q invalida; validas: localizar, actuar, verificar", r.Fase)
		}
		a.Phase = p
	}
	w, h := v.Size()
	point := func(required bool) error {
		if r.X == nil || r.Y == nil {
			if !required {
				return nil
			}
			if r.X == nil {
				return perr("x", "falta")
			}
			return perr("y", "falta")
		}
		x, y := int(math.Round(*r.X)), int(math.Round(*r.Y))
		if x < 0 || x >= w {
			return perr("x", "%d fuera de la imagen (0..%d)", x, w-1)
		}
		if y < 0 || y >= h {
			return perr("y", "%d fuera de la imagen (0..%d)", y, h-1)
		}
		a.ImgX, a.ImgY, a.HasXY = x, y, true
		a.X, a.Y = v.ToScreen(x, y)
		return nil
	}
	switch a.Kind {
	case KindClick, KindDoubleClick, KindHover:
		if err := point(true); err != nil {
			return Action{}, err
		}
	case KindType:
		if r.Text == nil {
			return Action{}, perr("text", "falta")
		}
		a.Text = *r.Text
		a.Replace = r.Replace == nil || *r.Replace
		if err := point(a.Replace); err != nil {
			if a.Replace && (r.X == nil || r.Y == nil) {
				pe := err.(*ParseError)
				pe.Msg = "falta (type con replace=true necesita x,y del campo)"
				return Action{}, pe
			}
			return Action{}, err
		}
		if strings.ContainsAny(a.Text, "\n\r") {
			return Action{}, perr("text", "no puede tener saltos de linea; usa key enter")
		}
	case KindKey:
		a.Combo = strings.TrimSpace(r.Combo)
		if a.Combo == "" {
			return Action{}, perr("combo", "falta")
		}
		if _, err := screen.ParseCombo(a.Combo); err != nil {
			return Action{}, perr("combo", "%v", err)
		}
	case KindScroll:
		if err := point(true); err != nil {
			return Action{}, err
		}
		a.Dir = strings.ToLower(strings.TrimSpace(r.Direction))
		switch a.Dir {
		case "up", "down", "left", "right":
		case "":
			return Action{}, perr("direction", "falta (up, down, left, right)")
		default:
			return Action{}, perr("direction", "%q invalida (up, down, left, right)", r.Direction)
		}
		a.Amount = 3
		if r.Amount != nil {
			a.Amount = int(math.Round(*r.Amount))
		}
		if a.Amount < 1 || a.Amount > 20 {
			return Action{}, perr("amount", "%d fuera de 1..20", a.Amount)
		}
	case KindZoom:
		if err := point(true); err != nil {
			return Action{}, err
		}
		if r.W == nil {
			return Action{}, perr("w", "falta")
		}
		if r.H == nil {
			return Action{}, perr("h", "falta")
		}
		rw, rh := int(math.Round(*r.W)), int(math.Round(*r.H))
		if rw < 8 || a.ImgX+rw > w {
			return Action{}, perr("w", "%d invalido: la region debe medir al menos 8 y caber en la imagen (x+w <= %d)", rw, w)
		}
		if rh < 8 || a.ImgY+rh > h {
			return Action{}, perr("h", "%d invalido: la region debe medir al menos 8 y caber en la imagen (y+h <= %d)", rh, h)
		}
		x0, y0 := v.ToScreen(a.ImgX, a.ImgY)
		x1, y1 := v.ToScreen(a.ImgX+rw-1, a.ImgY+rh-1)
		a.Region = image.Rect(x0, y0, x1+1, y1+1)
	case KindExtract:
		a.What = strings.TrimSpace(r.What)
		if a.What == "" {
			return Action{}, perr("what", "falta (que filas extraer)")
		}
		a.KeyCol = strings.TrimSpace(r.Key)
		a.Purpose = strings.ToLower(strings.TrimSpace(r.Purpose))
		if a.Purpose == "" {
			a.Purpose = "datos"
		}
		switch a.Purpose {
		case "conteo", "suma", "candidatos", "datos":
		default:
			return Action{}, perr("purpose", "%q invalido (conteo, suma, candidatos, datos)", r.Purpose)
		}
		if len(r.Filters) > 0 {
			a.Filters = map[string]string{}
			for k, fv := range r.Filters {
				a.Filters[k] = strings.TrimSpace(fmt.Sprint(fv))
			}
		}
	case KindCommit:
		if err := point(true); err != nil {
			return Action{}, err
		}
		a.What = strings.TrimSpace(r.What)
		if a.What == "" {
			return Action{}, perr("what", "falta (que persiste el clic)")
		}
		a.Target = strings.TrimSpace(r.Target)
		if a.Target == "" {
			return Action{}, perr("target", "falta (identificador del registro afectado)")
		}
		a.ConfirmDialog = r.Dialogo != nil && *r.Dialogo
	case KindFinish:
		f := &FinishRequest{Reason: firstNonEmpty(r.Razon, r.Reason), Aggregate: strings.ToLower(strings.TrimSpace(r.Agregado)),
			Entity: strings.TrimSpace(r.Entidad), EntityID: strings.TrimSpace(r.EntidadID), Changed: r.Cambios,
			Evidence: strings.TrimSpace(r.Evidencia)}
		f.Status = strings.ToLower(strings.TrimSpace(r.Estado))
		if f.Status != StatusDone && f.Status != StatusFailed {
			return Action{}, perr("estado", "%q invalido (terminada o fallida)", r.Estado)
		}
		data, err := stringList(r.Datos)
		if err != nil {
			return Action{}, perr("datos", "%v", err)
		}
		f.Data = data
		for _, raw := range r.Ident {
			s, err := scalarString(raw)
			if err != nil {
				return Action{}, perr("identificadores", "%v", err)
			}
			if s != "" {
				f.Identifiers = append(f.Identifiers, s)
			}
		}
		if f.Aggregate != "" && f.Aggregate != "conteo" && !strings.HasPrefix(f.Aggregate, "suma:") {
			return Action{}, perr("agregado", "%q invalido (conteo o suma:<columna>)", r.Agregado)
		}
		if r.Verificado != nil {
			f.Verified = *r.Verificado
		}
		if f.Reason == "" {
			return Action{}, perr("razon", "falta")
		}
		a.Finish = f
	}
	return a, nil
}

func validKind(k Kind) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

func kindList() string {
	s := make([]string, len(Kinds))
	for i, k := range Kinds {
		s[i] = string(k)
	}
	return strings.Join(s, ", ")
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if s := strings.TrimSpace(x); s != "" {
			return s
		}
	}
	return ""
}

// stringList acepta null, un escalar o una lista de escalares.
func stringList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var xs []json.RawMessage
	if err := json.Unmarshal(raw, &xs); err != nil {
		s, err := scalarString(raw)
		if err != nil {
			return nil, err
		}
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}
	var out []string
	for _, x := range xs {
		s, err := scalarString(x)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func scalarString(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s), nil
	}
	var n json.Number
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if d.Decode(&n) == nil {
		return n.String(), nil
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return fmt.Sprint(b), nil
	}
	return "", errors.New("debe ser texto o numero")
}

func shorten(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
