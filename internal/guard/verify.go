package guard

// 7.4: el estado final lo decide la verificacion en codigo. La relectura
// viene de las paginas extraidas despues del ultimo commit (lector aparte) o,
// para una edicion sin extraccion, de la evidencia que el modelo leyo en
// pantalla. Comparacion exacta salvo espacios extremos.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"screenoperator/internal/agent"
)

// ReviewFinish pide otro intento (una vez por motivo) antes de aceptar un
// cierre que el codigo todavia no puede respaldar.
func (g *Guard) ReviewFinish(st *agent.State, f *agent.FinishRequest) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	once := func(k, hint string) string {
		if hint == "" || g.reviews[k] > 0 {
			return ""
		}
		g.reviews[k]++
		return hint
	}
	c := st.Class
	if f.Status == agent.StatusFailed {
		if notFound(f.Reason) && c.Target != "" {
			if h := g.searchVariantsHint(c.Target); h != "" {
				return once("variants", "Antes de cerrar como 'no existe': "+h+".")
			}
		}
		return ""
	}
	if st.Phase != agent.PhaseVerify {
		return ""
	}
	if g.pending {
		return once("dialog", "Hay un diálogo de confirmación pendiente del último commit: el cambio no se guardó todavía. Si describe exactamente la acción pedida, confírmalo con commit \"dialogo\":true; luego verifica.")
	}
	switch c.Intent {
	case agent.IntentCreate:
		if !g.readAfterCommit() {
			return once("read", "Para verificar la creación, busca el registro por su identificador y usa extract con purpose candidatos sobre los resultados: el código exige exactamente uno.")
		}
	case agent.IntentExport:
		if !g.readAfterCommit() {
			return once("read", "Para verificar el exporte, abre la lista de reportes generados y usa extract con purpose datos: el código exige un único reporte nuevo con los filtros pedidos.")
		}
	}
	if c.Question || f.Aggregate != "" {
		if why := g.incomplete(); why != "" {
			return once("pages", "La extracción no está completa ("+why+"): avanza a la página que falta y vuelve a extraer.")
		}
	}
	return ""
}

func notFound(reason string) bool {
	r := fold(reason)
	for _, k := range []string{"no existe", "no se encontr", "no encontr", "no aparece", "sin resultados", "ningun resultado", "no hay registros", "cero candidatos", "0 candidatos"} {
		if strings.Contains(r, k) {
			return true
		}
	}
	return false
}

// BeforeFinish decide el estado final.
func (g *Guard) BeforeFinish(st *agent.State, f *agent.FinishRequest) agent.Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := st.Class
	g.red.LearnText(f.Evidence)
	if f.Status == agent.StatusFailed {
		var ids []string
		reason := f.Reason
		if cands, ok := g.candidates(st); ok && len(cands) > 1 {
			ids = candidateIDs(cands)
		}
		if notFound(reason) && c.Target != "" && !strings.Contains(fold(reason), fold(c.Target)) {
			reason += fmt.Sprintf("; no existe un registro que coincida con %q", c.Target)
			ids = append(ids, c.Target)
		}
		if reason != f.Reason || len(ids) > 0 {
			return agent.Decision{Deny: true, Reason: reason, Identifiers: uniq(ids)}
		}
		return agent.Allow
	}
	if g.pending {
		return agent.Deny("el último guardado quedó pendiente de un diálogo de confirmación: el cambio no persistió")
	}
	if !g.pol.Writes.VerifyAfterWrite && !c.Question {
		return agent.Allow
	}
	switch c.Intent {
	case agent.IntentEdit:
		res, why, ok := g.verifyEdit(st, f)
		if !ok {
			return agent.Deny("no se pudo leer el registro para verificar el cambio; no se da por terminada")
		}
		if res != "ok" {
			return agent.Deny("la verificación no coincide: %s", why)
		}
	case agent.IntentCreate:
		if d := g.verifyCreate(st, f); d.Deny {
			return d
		}
	case agent.IntentExport:
		if d := g.verifyExport(st, f); d.Deny {
			return d
		}
	}
	if c.Question || f.Aggregate != "" {
		if why := g.incomplete(); why != "" {
			return agent.Deny("la extracción de la lista quedó incompleta (%s): no se responde con un dato parcial", why)
		}
	}
	return agent.Allow
}

func (g *Guard) readAfterCommit() bool {
	for _, o := range g.pages {
		if g.seq > 0 && o.after >= g.seq {
			return true
		}
	}
	return false
}

// postRows son las filas leidas despues del ultimo commit.
func (g *Guard) postRows() ([]agent.Row, [][]string) {
	var rows []agent.Row
	var cols [][]string
	for _, o := range g.pages {
		if g.seq > 0 && o.after >= g.seq {
			rows = append(rows, o.page.Rows...)
			for range o.page.Rows {
				cols = append(cols, o.page.Columns)
			}
		}
	}
	return rows, cols
}

// verifyEdit compara cada valor pedido con lo leido despues del commit.
// res: "ok" o "fallo"; ok=false si no hubo nada que leer.
func (g *Guard) verifyEdit(st *agent.State, f *agent.FinishRequest) (res, why string, ok bool) {
	c := st.Class
	rows, cols := g.postRows()
	evidence := ""
	if f != nil {
		evidence = strings.TrimSpace(f.Evidence)
	}
	if len(rows) == 0 && evidence == "" {
		return "", "", false
	}
	checked := 0
	var bad []string
	keys := make([]string, 0, len(c.Values))
	for k := range c.Values {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, field := range keys {
		want := strings.TrimSpace(c.Values[field])
		if want == "" || (c.Target != "" && strings.Contains(fold(c.Target), fold(want))) {
			continue // el nombre del objetivo no es un cambio
		}
		if got, found := readField(rows, cols, field, c.Target); found {
			checked++
			if strings.TrimSpace(got) != want {
				bad = append(bad, fmt.Sprintf("%s: esperado %q, leído %q", field, want, strings.TrimSpace(got)))
			}
			continue
		}
		if evidence != "" {
			checked++
			if !containsBounded(evidence, want) {
				bad = append(bad, fmt.Sprintf("%s: esperado %q, leído %q", field, want, truncate(evidence, 120)))
			}
		}
	}
	if checked == 0 {
		if f == nil {
			return "", "", false
		}
		if evidence == "" {
			return "", "", false
		}
		return "ok", "", true
	}
	if len(bad) > 0 {
		return "fallo", strings.Join(bad, "; "), true
	}
	return "ok", "", true
}

// readField busca el valor de un campo en las filas leidas: como columna
// (en la fila del objetivo si hay varias) o en tablas campo/valor.
func readField(rows []agent.Row, cols [][]string, field, target string) (string, bool) {
	ff := fold(field)
	same := func(label string) bool {
		fl := fold(label)
		return fl == ff || strings.ReplaceAll(fl, " ", "_") == ff || strings.ReplaceAll(ff, "_", " ") == fl
	}
	// campo/valor: una celda es la etiqueta y la siguiente columna el valor.
	for i, r := range rows {
		cs := cols[i]
		for j := 0; j+1 < len(cs); j++ {
			if lab, ok := r[cs[j]]; ok && same(lab) {
				return r[cs[j+1]], true
			}
		}
	}
	var hits []agent.Row
	for _, r := range rows {
		for k := range r {
			if same(k) {
				hits = append(hits, r)
				break
			}
		}
	}
	if len(hits) > 1 && target != "" {
		if m := matchTarget(hits, target, lowerExact); len(m) > 0 {
			hits = m
		}
	}
	if len(hits) == 1 || (len(hits) > 1 && target != "") {
		for k, v := range hits[0] {
			if same(k) {
				return v, true
			}
		}
	}
	return "", false
}

// verifyCreate exige exactamente un registro con el identificador tras el commit.
func (g *Guard) verifyCreate(st *agent.State, f *agent.FinishRequest) agent.Decision {
	c := st.Class
	ids := classIDs(c)
	rows, _ := g.postRows()
	evidence := strings.TrimSpace(f.Evidence)
	if len(ids) == 0 && c.Target != "" {
		ids = []string{c.Target}
	}
	if len(rows) == 0 {
		if evidence == "" {
			return agent.Deny("no se pudo leer el registro creado para verificarlo; no se da por terminada")
		}
		for _, id := range ids {
			if strings.Contains(evidence, id) || (digits(id) != "" && strings.Contains(digits(evidence), digits(id))) {
				return agent.Allow
			}
		}
		return agent.Deny("la verificación no encontró el registro creado (%s) en lo leído", strings.Join(ids, ", "))
	}
	for _, id := range ids {
		var match []agent.Row
		seen := map[string]bool{}
		for _, r := range rows {
			if rowHasID(r, id, digits(id)) || (!isID(id) && len(matchTarget([]agent.Row{r}, id, lowerExact)) > 0) {
				s := rowSig(r, "")
				if !seen[s] {
					seen[s] = true
					match = append(match, r)
				}
			}
		}
		switch {
		case len(match) > 1:
			codes := candidateIDs(match)
			return agent.Fail(codes, "hay %d registros con %s: registro duplicado (%s)", len(match), id, strings.Join(codes, ", "))
		case len(match) == 0:
			return agent.Deny("la verificación no encontró el registro creado con %s", id)
		}
	}
	return agent.Allow
}

// verifyExport exige un unico reporte nuevo con los filtros pedidos.
func (g *Guard) verifyExport(st *agent.State, f *agent.FinishRequest) agent.Decision {
	post, postCols := g.postRows()
	if len(post) == 0 {
		if strings.TrimSpace(f.Evidence) == "" {
			return agent.Deny("no se pudo leer la lista de reportes para verificar el exporte; no se da por terminada")
		}
		return agent.Allow
	}
	// Filas que ya estaban antes del commit en una lista con las mismas columnas.
	before := map[string]bool{}
	hadBefore := false
	for _, o := range g.pages {
		if o.after >= g.seq || len(postCols) == 0 || !sameCols(o.page.Columns, postCols[0]) {
			continue
		}
		hadBefore = true
		for _, r := range o.page.Rows {
			before[rowSig(r, "")] = true
		}
	}
	var fresh []agent.Row
	seen := map[string]bool{}
	for _, r := range post {
		s := rowSig(r, "")
		if before[s] || seen[s] {
			continue
		}
		seen[s] = true
		fresh = append(fresh, r)
	}
	var want []string
	for _, v := range st.Class.Values {
		if v = strings.TrimSpace(v); v != "" && digits(v) == "" && !isMonthish(v) {
			want = append(want, v)
		}
	}
	matching := fresh[:0:0]
	for _, r := range fresh {
		if rowHasAll(r, want) {
			matching = append(matching, r)
		}
	}
	switch {
	case hadBefore && len(fresh) > 1:
		return agent.Fail(candidateIDs(fresh), "aparecieron %d reportes nuevos: exporte duplicado", len(fresh))
	case hadBefore && len(fresh) == 0:
		return agent.Deny("la lista de reportes no muestra un reporte nuevo")
	case len(matching) == 0:
		return agent.Deny("ningún reporte nuevo tiene los filtros pedidos (%s)", strings.Join(want, ", "))
	case !hadBefore && len(matching) > 1:
		// Sin lectura previa no se distingue lo nuevo de lo viejo: no se afirma duplicado.
		return agent.Allow
	}
	return agent.Allow
}

func sameCols(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if fold(a[i]) != fold(b[i]) {
			return false
		}
	}
	return true
}

func rowHasAll(r agent.Row, want []string) bool {
	all := fold(strings.Join(sortedValues(r), " | "))
	for _, w := range want {
		if !strings.Contains(all, fold(w)) {
			return false
		}
	}
	return true
}

var months = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "setiembre", "octubre", "noviembre", "diciembre", "hoy", "ayer", "semana", "mes"}

func isMonthish(v string) bool { return wordHit(fold(v), months) != "" }

// rePage exige la palabra pagina (o el indicador entero "2 de 3"): "Mostrando
// 1-20 de 45" es un total de filas, no de paginas.
var rePage = regexp.MustCompile(`(?i)(?:p(?:[aá]g(?:ina)?|age)\.?\s*(\d+)\s*(?:de|/|of)\s*(\d+)|^\s*(\d+)\s*(?:de|/|of)\s*(\d+)\s*$)`)

// incomplete revisa los indicadores de pagina de la ultima lista extraida:
// si dice "pagina X de Y" y no se leyeron las Y paginas, la extraccion esta
// incompleta (falso exito medido en E3: 27 en vez de 45).
func (g *Guard) incomplete() string {
	if len(g.pages) == 0 {
		return ""
	}
	last := g.pages[len(g.pages)-1]
	if last.page.NextVisible && last.page.NextEnabled && len(last.page.Rows) > 0 {
		// La ultima pagina leida todavia ofrece "siguiente": una pagina
		// repetida (un clic que no avanzo) no es el final de la lista
		// (falso exito medido en E3: 29 y 27 en vez de 45).
		return "la última página leída todavía tiene 'siguiente' habilitado"
	}
	total := 0
	seen := map[int]bool{}
	for i := len(g.pages) - 1; i >= 0; i-- {
		o := g.pages[i]
		if o.purpose != last.purpose || !strings.EqualFold(o.key, last.key) {
			break
		}
		m := rePage.FindStringSubmatch(o.page.Indicator)
		if m == nil {
			continue
		}
		a, b := m[1], m[2]
		if a == "" {
			a, b = m[3], m[4]
		}
		n, _ := strconv.Atoi(a)
		t, _ := strconv.Atoi(b)
		if t <= 0 || n <= 0 || n > t || t > 200 {
			continue
		}
		if total == 0 {
			total = t
		} else if t != total {
			break // otra lista u otro filtro
		}
		seen[n] = true
	}
	if total == 0 || len(seen) >= total {
		return ""
	}
	var miss []string
	for p := 1; p <= total; p++ {
		if !seen[p] {
			miss = append(miss, strconv.Itoa(p))
		}
	}
	return fmt.Sprintf("el indicador dice %d páginas y falta leer la página %s", total, strings.Join(miss, ", "))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
