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
}

// ParsePage interpreta la respuesta del extractor.
func ParsePage(content string) (Page, error) {
	var raw struct {
		Columnas  []string         `json:"columnas"`
		Filas     []map[string]any `json:"filas"`
		Siguiente *struct {
			Visible    *bool `json:"visible"`
			Habilitado *bool `json:"habilitado"`
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
	for _, f := range raw.Filas {
		r := Row{}
		for k, v := range f {
			if v == nil {
				r[strings.TrimSpace(k)] = ""
				continue
			}
			r[strings.TrimSpace(k)] = strings.TrimSpace(fmt.Sprint(v))
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
	}
	if !p.NextVisible {
		p.NextEnabled = false
	}
	if raw.Pagina != nil {
		p.Indicator = strings.TrimSpace(fmt.Sprint(raw.Pagina))
	}
	return p, nil
}

// Aggregator acumula filas de una lista paginada.
type Aggregator struct {
	What    string
	KeyCol  string
	Purpose string
	Filters map[string]string

	rows    []Row
	seen    map[string]bool
	lastSig string
	pages   int
	Done    bool
	Stop    string // por que se detuvo la extraccion
}

// NewAggregator crea un acumulador. keyCol vacio = la fila completa es la clave.
func NewAggregator(what, keyCol, purpose string, filters map[string]string) *Aggregator {
	return &Aggregator{What: what, KeyCol: keyCol, Purpose: purpose, Filters: filters, seen: map[string]bool{}}
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

// AddPage agrega una pagina. Devuelve las filas nuevas y si la extraccion debe
// detenerse: boton siguiente deshabilitado o ausente, o pagina repetida.
func (g *Aggregator) AddPage(p Page) (added int, stop bool) {
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
	switch {
	case len(p.Rows) > 0 && sig == g.lastSig:
		g.Done, g.Stop = true, "la pagina nueva repite la anterior"
	case !p.NextEnabled:
		g.Done, g.Stop = true, "no hay pagina siguiente habilitada"
	}
	g.lastSig = sig
	return added, g.Done
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
	if g.Done {
		s += "; extraccion completa: " + g.Stop
	} else {
		s += "; hay pagina siguiente: avanza y vuelve a extraer"
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

func norm(s string) string { return strings.ToLower(spaces.ReplaceAllString(strings.TrimSpace(s), " ")) }

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
