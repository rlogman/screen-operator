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
// no avanzo, pero solo es completa si se vieron todas las paginas. Con
// indicador "Pagina N de M" completa = paginas vistas {1..M}. Sin indicador,
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
	advances int          // avances de pagina desde la ultima extraccion
	chainBad string       // sin indicador: por que la cadena de avances no es confiable
	restart  bool         // la proxima extraccion reinicia el acumulado

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

var reIndicator = regexp.MustCompile(`(?i)(\d+)\s*(?:de|of|/)\s*(\d+)`)

// ParseIndicator lee "Pagina N de M". ok=false si no hay indicador valido.
func ParseIndicator(s string) (n, m int, ok bool) {
	sm := reIndicator.FindStringSubmatch(s)
	if sm == nil {
		return 0, 0, false
	}
	n, _ = strconv.Atoi(sm[1])
	m, _ = strconv.Atoi(sm[2])
	if n < 1 || m < 1 || n > m {
		return 0, 0, false
	}
	return n, m, true
}

// AddPage agrega una pagina. Devuelve las filas nuevas y si la extraccion se
// detuvo (Stopped). Detenida no implica completa: ver Complete y Stop.
func (g *Aggregator) AddPage(p Page) (added int, stop bool) {
	adv := g.advances
	g.advances = 0
	if g.restart {
		*g = *NewAggregator(g.What, g.KeyCol, g.Purpose, g.Filters)
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
	n, m, hasInd := ParseIndicator(p.Indicator)
	repeated := !first && ((len(p.Rows) > 0 && sig == g.lastSig) || (hasInd && n == g.lastNum))
	g.lastSig = sig

	if hasInd {
		if m > g.total {
			g.total = m
		}
	} else if repeated {
		n = g.lastNum
	} else if g.lastNum > 0 && adv == 1 {
		n = g.lastNum + 1 // sin indicador en esta pagina: se deduce del avance unico
	} else {
		n = 0
	}
	if !first && !repeated && !hasInd && adv != 1 && g.total == 0 {
		if adv == 0 {
			g.chainBad = "entre dos extracciones no hubo un avance de pagina reconocido"
		} else {
			g.chainBad = fmt.Sprintf("entre dos extracciones hubo %d avances de pagina: pudo saltarse una pagina", adv)
		}
	}
	if n > 0 {
		g.pageSeen[n] = true
		g.lastNum = n
	} else if !repeated {
		g.lastNum = 0
	}

	g.Stopped = repeated || !p.NextEnabled
	g.Complete, g.Stop = g.completeness(p, repeated)
	return added, g.Stopped
}

// completeness decide si se vieron todas las paginas y explica por que no.
func (g *Aggregator) completeness(p Page, repeated bool) (bool, string) {
	if repeated && p.NextEnabled {
		return false, "la pagina no avanzo; reintenta siguiente"
	}
	if g.total > 0 {
		var missing []string
		for i := 1; i <= g.total; i++ {
			if !g.pageSeen[i] {
				missing = append(missing, strconv.Itoa(i))
			}
		}
		if len(missing) > 0 {
			return false, fmt.Sprintf("faltan las paginas %s de %d: navega a cada una y extraela", strings.Join(missing, ", "), g.total)
		}
		return true, fmt.Sprintf("se extrajeron las %d paginas", g.total)
	}
	if g.chainBad != "" {
		g.restart = true
		return false, g.chainBad + "; vuelve a la primera pagina de la lista y extrae de nuevo pagina por pagina (el acumulado se reinicia)"
	}
	if p.NextEnabled {
		return false, "hay pagina siguiente: avanza y vuelve a extraer"
	}
	if repeated {
		return true, "la pagina repite la anterior y no hay pagina siguiente habilitada"
	}
	return true, "no hay pagina siguiente habilitada"
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
