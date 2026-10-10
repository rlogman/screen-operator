package agent

// Extraccion estructurada y agregacion multipagina (6.5). El modelo lee las
// filas visibles como JSON; el codigo acumula entre paginas, deduplica por la
// columna clave, filtra, cuenta y suma. El modelo nunca cuenta.

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"screenoperator/internal/llm"
)

// Row es una fila extraida: columna -> valor como texto.
type Row map[string]string

// Page es lo que el modelo leyo de una pagina.
type Page struct {
	Columns     []string
	Rows        []Row
	NextVisible bool
	NextEnabled bool
	Indicator   string
	// NextX, NextY: centro del boton "siguiente" en la imagen del extractor.
	NextX, NextY int
	HasNextXY    bool
	// PrevDisabled: el lector informo que el boton "anterior" no existe o esta
	// deshabilitado (evidencia de primera pagina). Sin dato = false.
	PrevDisabled bool
}

// ParsePage interpreta la respuesta del extractor.
func ParsePage(content string) (Page, error) {
	var raw struct {
		Columnas  []string          `json:"columnas"`
		Filas     []json.RawMessage `json:"filas"`
		Siguiente *struct {
			Visible    *bool    `json:"visible"`
			Habilitado *bool    `json:"habilitado"`
			X          *float64 `json:"x"`
			Y          *float64 `json:"y"`
		} `json:"siguiente"`
		Anterior *struct {
			Visible    *bool `json:"visible"`
			Habilitado *bool `json:"habilitado"`
		} `json:"anterior"`
		Pagina any `json:"pagina"`
	}
	dec := json.NewDecoder(strings.NewReader(llm.StripFences(content)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Page{}, &ParseError{Msg: "extraccion no es JSON: " + shorten(err.Error())}
	}
	if raw.Filas == nil {
		return Page{}, perr("filas", "falta")
	}
	p := Page{Columns: raw.Columnas}
	for i, f := range raw.Filas {
		r := Row{}
		var obj map[string]any
		var list []any
		d := json.NewDecoder(strings.NewReader(string(f)))
		d.UseNumber()
		if err := d.Decode(&obj); err == nil {
			for k, v := range obj {
				r[strings.TrimSpace(k)] = cell(v)
			}
		} else {
			d = json.NewDecoder(strings.NewReader(string(f)))
			d.UseNumber()
			if err := d.Decode(&list); err != nil {
				return Page{}, perr("filas", "la fila %d no es una lista ni un objeto", i+1)
			}
			if len(raw.Columnas) == 0 {
				return Page{}, perr("columnas", "falta (las filas son listas)")
			}
			for j, v := range list {
				col := fmt.Sprintf("col%d", j+1)
				if j < len(raw.Columnas) {
					col = strings.TrimSpace(raw.Columnas[j])
				}
				r[col] = cell(v)
			}
		}
		p.Rows = append(p.Rows, r)
	}
	if raw.Siguiente != nil {
		if raw.Siguiente.Visible != nil {
			p.NextVisible = *raw.Siguiente.Visible
		}
		if raw.Siguiente.Habilitado != nil {
			p.NextEnabled = *raw.Siguiente.Habilitado
		}
		if raw.Siguiente.X != nil && raw.Siguiente.Y != nil {
			p.NextX, p.NextY, p.HasNextXY = int(*raw.Siguiente.X+0.5), int(*raw.Siguiente.Y+0.5), true
		}
	}
	if !p.NextVisible {
		p.NextEnabled = false
	}
	if a := raw.Anterior; a != nil {
		p.PrevDisabled = (a.Visible != nil && !*a.Visible) || (a.Habilitado != nil && !*a.Habilitado)
	}
	if raw.Pagina != nil {
		p.Indicator = strings.TrimSpace(fmt.Sprint(raw.Pagina))
	}
	return p, nil
}

func cell(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// Aggregator acumula filas de una lista paginada.
//
// Detenida (Stopped) y completa (Complete) son estados distintos: la
// extraccion se detiene cuando no hay pagina siguiente habilitada o la pagina
// no avanzo, pero solo es completa si se vieron todas las paginas. Con numero
// de pagina ("Pagina N de M" o "Pagina N") completa = paginas vistas {1..M},
// donde M viene del indicador o de la ultima pagina (sin siguiente). Sin numero,
// cada extraccion debe estar separada de la anterior por exactamente un avance
// de pagina (NoteAdvance) y la ultima debe tener "siguiente" deshabilitado.
// El cierre con agregado exige Complete.
type Aggregator struct {
	What    string
	KeyCol  string
	Purpose string
	Filters map[string]string

	rows    []Row
	seen    map[string]bool
	lastSig string
	pages   int

	pageSeen map[int]bool // numeros de pagina extraidos (por indicador)
	total    int          // M del indicador; 0 = desconocido
	lastNum  int          // numero de la ultima pagina extraida; 0 = desconocido
	maxNum   int          // mayor numero de pagina extraido
	numbered bool         // la app muestra numero de pagina
	advances int          // avances de pagina desde la ultima extraccion
	chainBad string       // sin indicador: por que la cadena de avances no es confiable
	restart  bool         // la proxima extraccion reinicia el acumulado
	firstSig string       // sin indicador: firma de la primera pagina verificada de la cadena
	firstSet bool         // firstSig fue fijada (sobrevive a los reinicios)

	Stopped  bool   // no hay que seguir avanzando (o la pagina no avanzo)
	Complete bool   // se vieron todas las paginas
	Stop     string // por que se detuvo o por que esta incompleta
}

// NewAggregator crea un acumulador. keyCol vacio = la fila completa es la clave.
func NewAggregator(what, keyCol, purpose string, filters map[string]string) *Aggregator {
	return &Aggregator{What: what, KeyCol: keyCol, Purpose: purpose, Filters: filters, seen: map[string]bool{}, pageSeen: map[int]bool{}}
}

// Same indica si una extraccion nueva continua la misma lista.
func (g *Aggregator) Same(keyCol, purpose string, filters map[string]string) bool {
	if !strings.EqualFold(g.KeyCol, keyCol) || g.Purpose != purpose {
		return false
	}
	if len(filters) == 0 {
		return true
	}
	return fmt.Sprint(sortedMap(filters)) == fmt.Sprint(sortedMap(g.Filters))
}

// NoteAdvance registra un avance de pagina (clic en "siguiente" con efecto)
// entre dos extracciones.
func (g *Aggregator) NoteAdvance() { g.advances++ }

var (
	rePageOf    = regexp.MustCompile(`(?i)p(?:á|a)g(?:ina|e)?\.?\s*(\d+)\s*(?:de|of|/)\s*(\d+)`)
	reIndicator = regexp.MustCompile(`(?i)(\d+)\s*(?:de|of|/)\s*(\d+)`)
	reRangeHead = regexp.MustCompile(`(?i)\d+\s*(?:-|–|\ba|\bto)\s*$`)
	rePageOnly  = regexp.MustCompile(`(?i)p(?:á|a)g(?:ina|e)?\.?\s*(\d+)`)
)

// ParseIndicator lee el indicador de pagina: "Pagina N de M" da n y m;
// "Pagina N" da solo n (m=0). ok=false si no hay numero de pagina. Un rango
// de registros ("Mostrando 1-5 de 5", "Registros 1 a 20 de 45") no es un
// numero de pagina: esa lista se trata como no numerada.
func ParseIndicator(s string) (n, m int, ok bool) {
	valid := func(a, b string) bool {
		n, _ = strconv.Atoi(a)
		m, _ = strconv.Atoi(b)
		return n >= 1 && m >= 1 && n <= m
	}
	if sm := rePageOf.FindStringSubmatch(s); sm != nil && valid(sm[1], sm[2]) {
		return n, m, true
	}
	for _, ix := range reIndicator.FindAllStringSubmatchIndex(s, -1) {
		if reRangeHead.MatchString(s[:ix[0]]) {
			continue // "1-5 de 5": es un rango de registros
		}
		if valid(s[ix[2]:ix[3]], s[ix[4]:ix[5]]) {
			return n, m, true
		}
	}
	n, m = 0, 0
	if sm := rePageOnly.FindStringSubmatch(s); sm != nil {
		n, _ = strconv.Atoi(sm[1])
		if n >= 1 {
			return n, 0, true
		}
	}
	return 0, 0, false
}

// AddPage agrega una pagina. Devuelve las filas nuevas y si la extraccion se
// detuvo (Stopped). Detenida no implica completa: ver Complete y Stop.
func (g *Aggregator) AddPage(p Page) (added int, stop bool) {
	adv := g.advances
	g.advances = 0
	if g.restart {
		fs, fset := g.firstSig, g.firstSet
		*g = *NewAggregator(g.What, g.KeyCol, g.Purpose, g.Filters)
		g.firstSig, g.firstSet = fs, fset
		adv = 0
	}
	first := g.pages == 0
	g.pages++
	keys := make([]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		k := g.rowKey(r)
		keys = append(keys, k)
		if g.seen[k] {
			continue
		}
		g.seen[k] = true
		g.rows = append(g.rows, r)
		added++
	}
	sort.Strings(keys)
	sig := strings.Join(keys, "\x00")
	n, m, hasNum := ParseIndicator(p.Indicator)
	repeated := !first && ((len(p.Rows) > 0 && sig == g.lastSig) || (hasNum && n == g.lastNum))
	g.lastSig = sig

	// Un numero de pagina solo cuenta si se leyo: nunca se deduce de un
	// avance (el clic pudo saltar paginas), ni para marcar la pagina vista ni
	// para fijar el total.
	switch {
	case hasNum:
		g.numbered = true
	case repeated:
		n = g.lastNum
	default:
		n = 0
	}
	if m > g.total {
		g.total = m
	}
	if hasNum && !p.NextEnabled && n > g.total {
		g.total = n // la ultima pagina (sin siguiente), con su numero leido, fija el total
	}
	if first && !hasNum {
		// Sin numero de pagina: la cadena debe empezar en la primera pagina,
		// con "anterior" deshabilitado o ausente y, tras un reinicio, con las
		// mismas filas que la primera pagina verificada.
		switch {
		case !p.PrevDisabled:
			g.chainBad = "no consta que esta sea la primera pagina de la lista (el boton anterior esta habilitado o no se leyo su estado)"
		case g.firstSet && sig != g.firstSig:
			g.chainBad = "no volviste a la primera pagina: sus filas no coinciden con las de la primera pagina extraida"
		case !g.firstSet:
			g.firstSig, g.firstSet = sig, true
		}
	}
	if !g.numbered && !first && !repeated && adv != 1 {
		if adv == 0 {
			g.chainBad = "entre dos extracciones no hubo un avance de pagina reconocido"
		} else {
			g.chainBad = fmt.Sprintf("entre dos extracciones hubo %d avances de pagina: pudo saltarse una pagina", adv)
		}
	}
	if n > 0 {
		g.pageSeen[n] = true
		if n > g.maxNum {
			g.maxNum = n
		}
	}
	g.lastNum = n

	g.Stopped = repeated || !p.NextEnabled
	g.Complete, g.Stop = g.completeness(p, repeated)
	return added, g.Stopped
}

// missingUpTo lista las paginas 1..k que no se extrajeron.
func (g *Aggregator) missingUpTo(k int) []string {
	var out []string
	for i := 1; i <= k; i++ {
		if !g.pageSeen[i] {
			out = append(out, strconv.Itoa(i))
		}
	}
	return out
}

// completeness decide si se vieron todas las paginas y explica por que no.
func (g *Aggregator) completeness(p Page, repeated bool) (bool, string) {
	if repeated && p.NextEnabled {
		return false, "la pagina no avanzo; reintenta siguiente"
	}
	if g.numbered {
		upTo := g.maxNum
		if g.total > 0 {
			upTo = g.total
		}
		if miss := g.missingUpTo(upTo); len(miss) > 0 {
			of := ""
			if g.total > 0 {
				of = fmt.Sprintf(" de %d", g.total)
			}
			return false, fmt.Sprintf("faltan las paginas %s%s: vuelve a cada una (anterior o su numero), extraela y sigue; lo ya extraido se conserva y los filtros no se tocan", strings.Join(miss, ", "), of)
		}
		if g.total > 0 {
			return true, fmt.Sprintf("se extrajeron las %d paginas", g.total)
		}
		if !p.NextEnabled {
			return false, "no se leyo el numero de esta ultima pagina (debe verse 'Pagina N'): extraela de nuevo"
		}
		return false, "hay pagina siguiente: haz UN clic en siguiente y extrae esa pagina"
	}
	if g.chainBad != "" {
		g.restart = true
		return false, g.chainBad + "; vuelve a la primera pagina de la lista (sin cambiar los filtros) y extrae de nuevo pagina por pagina (el acumulado se reinicia)"
	}
	if p.NextEnabled {
		return false, "hay pagina siguiente: haz UN clic en siguiente y extrae esa pagina"
	}
	if repeated {
		return true, "la pagina repite la anterior y no hay pagina siguiente habilitada"
	}
	return true, "no hay pagina siguiente habilitada"
}

var reQuantity = regexp.MustCompile(`\b(?:cuantos|cuantas|total\w*|suma\w*|cantidad(?:es)?)\b`)

// AsksQuantity indica si la orden (o los datos pedidos) piden una cantidad,
// un total o una suma: "cuantos", "cuantas", "total", "suma", "cantidad", con
// o sin tildes.
func AsksQuantity(instruction string, asked []string) bool {
	t := strings.ToLower(instruction + " " + strings.Join(asked, " "))
	t = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "Á", "a", "Ú", "u", "Ó", "o").Replace(t)
	return reQuantity.MatchString(t)
}

func (g *Aggregator) rowKey(r Row) string {
	if g.KeyCol != "" {
		if v, ok := lookupCol(r, g.KeyCol); ok && v != "" {
			return "k:" + norm(v)
		}
	}
	return "r:" + fmt.Sprint(sortedMap(r))
}

// Rows devuelve las filas unicas acumuladas.
func (g *Aggregator) Rows() []Row { return append([]Row(nil), g.rows...) }

// Pages es la cantidad de paginas agregadas.
func (g *Aggregator) Pages() int { return g.pages }

// Matching devuelve las filas que cumplen los filtros (columna sin distinguir
// mayusculas; valor igual tras recortar espacios y sin distinguir mayusculas,
// con tildes exactas: "Ramírez" y "Ramirez" son registros distintos). Un
// filtro sobre una columna que no aparece en ninguna fila se ignora y se
// informa en missing.
func (g *Aggregator) Matching() (rows []Row, missing []string) {
	present := map[string]bool{}
	for col := range g.Filters {
		for _, r := range g.rows {
			if _, ok := lookupCol(r, col); ok {
				present[col] = true
				break
			}
		}
		if !present[col] {
			missing = append(missing, col)
		}
	}
	sort.Strings(missing)
	for _, r := range g.rows {
		ok := true
		for col, want := range g.Filters {
			if !present[col] {
				continue
			}
			v, _ := lookupCol(r, col)
			if norm(v) != norm(want) {
				ok = false
				break
			}
		}
		if ok {
			rows = append(rows, r)
		}
	}
	return rows, missing
}

// Count es la cantidad de filas unicas que cumplen los filtros.
func (g *Aggregator) Count() int {
	rows, _ := g.Matching()
	return len(rows)
}

// Sum suma la columna col de las filas que cumplen los filtros.
func (g *Aggregator) Sum(col string) (float64, error) {
	rows, _ := g.Matching()
	var total float64
	for _, r := range rows {
		v, ok := lookupCol(r, col)
		if !ok {
			return 0, fmt.Errorf("la columna %q no aparece en las filas extraidas", col)
		}
		n, err := ParseAmount(v)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Summary describe el acumulado para el historial del modelo.
func (g *Aggregator) Summary() string {
	n, missing := g.Matching()
	s := fmt.Sprintf("%q: %d filas unicas en %d paginas; %d cumplen los filtros %v", g.What, len(g.rows), g.pages, len(n), g.Filters)
	if len(missing) > 0 {
		s += fmt.Sprintf(" (columnas de filtro no vistas: %v)", missing)
	}
	switch {
	case g.Complete:
		s += "; extraccion completa: " + g.Stop
	default:
		s += "; extraccion INCOMPLETA: " + g.Stop
	}
	return s
}

func lookupCol(r Row, col string) (string, bool) {
	if v, ok := r[col]; ok {
		return v, true
	}
	for k, v := range r {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(col)) {
			return v, true
		}
	}
	return "", false
}

var spaces = regexp.MustCompile(`\s+`)

func norm(s string) string {
	return strings.ToLower(spaces.ReplaceAllString(strings.TrimSpace(s), " "))
}

func sortedMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

var reAmount = regexp.MustCompile(`[-]?[0-9][0-9.,]*`)

// ParseAmount interpreta un valor de la app en formato colombiano: punto como
// separador de miles y coma decimal ("6.980.000" = 6980000, "1.234,5" =
// 1234.5). Ignora simbolos de moneda y espacios. Un solo punto seguido de
// exactamente tres digitos tambien es de miles ("45.000" = 45000).
func ParseAmount(s string) (float64, error) {
	m := reAmount.FindString(strings.ReplaceAll(s, " ", ""))
	if m == "" {
		return 0, fmt.Errorf("%q no tiene un numero", s)
	}
	neg := strings.HasPrefix(m, "-")
	m = strings.TrimPrefix(m, "-")
	m = strings.TrimRight(m, ".,")
	intPart, frac := m, ""
	if i := strings.LastIndex(m, ","); i >= 0 {
		intPart, frac = m[:i], m[i+1:]
	} else if strings.Count(m, ".") == 1 {
		// "12.5" (decimal) vs "45.000" (miles): tres digitos tras el punto = miles.
		i := strings.Index(m, ".")
		if len(m)-i-1 != 3 {
			intPart, frac = m[:i], m[i+1:]
		}
	}
	intPart = strings.ReplaceAll(strings.ReplaceAll(intPart, ".", ""), ",", "")
	num := intPart
	if frac != "" {
		num += "." + frac
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("%q no es un numero valido", s)
	}
	if neg {
		f = -f
	}
	return f, nil
}

// FormatNumber escribe un numero sin separadores de miles; enteros sin decimales.
func FormatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
