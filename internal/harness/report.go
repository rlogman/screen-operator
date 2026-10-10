package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Categorias por defecto de los registros sin campo "categoria".
const (
	CategoriaOficial      = "oficial"
	CategoriaSinCategoria = "sin categoria"
)

// Goals son las metas del diseño, leidas del archivo de configuracion del
// harness (cmd/harness/goals.yaml o --goals).
type Goals struct {
	Oficiales struct {
		// Prefijo de ruta que identifica un caso oficial.
		Prefijo  string `yaml:"prefijo" json:"prefijo"`
		Casos    int    `yaml:"casos" json:"casos"`
		Corridas int    `yaml:"corridas" json:"corridas"`
	} `yaml:"oficiales" json:"oficiales"`
	Propios struct {
		AciertoMin float64 `yaml:"acierto_min" json:"acierto_min"`
	} `yaml:"propios" json:"propios"`
	FalsosExitosMax  int     `yaml:"falsos_exitos_max" json:"falsos_exitos_max"`
	SecretosMax      int     `yaml:"secretos_filtrados_max" json:"secretos_filtrados_max"`
	P95MaxS          float64 `yaml:"p95_max_s" json:"p95_max_s"`
	CostoMedioMaxUSD float64 `yaml:"costo_medio_max_usd" json:"costo_medio_max_usd"`
}

// ParseGoals lee las metas (YAML estricto: una clave desconocida es error).
func ParseGoals(b []byte) (Goals, error) {
	var g Goals
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&g); err != nil {
		return Goals{}, fmt.Errorf("metas: %w", err)
	}
	switch {
	case g.Oficiales.Prefijo == "":
		return Goals{}, fmt.Errorf("metas: falta oficiales.prefijo")
	case g.Oficiales.Casos < 1 || g.Oficiales.Corridas < 1:
		return Goals{}, fmt.Errorf("metas: oficiales.casos y oficiales.corridas deben ser >= 1")
	case g.Propios.AciertoMin < 0 || g.Propios.AciertoMin > 1:
		return Goals{}, fmt.Errorf("metas: propios.acierto_min va de 0 a 1")
	case g.P95MaxS <= 0 || g.CostoMedioMaxUSD <= 0:
		return Goals{}, fmt.Errorf("metas: p95_max_s y costo_medio_max_usd deben ser > 0")
	}
	return g, nil
}

// ReadRecords lee uno o mas JSONL de resultados.
func ReadRecords(paths ...string) ([]Record, error) {
	var out []Record
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
		n := 0
		for sc.Scan() {
			n++
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var r Record
			if err := json.Unmarshal(line, &r); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s:%d: %w", p, n, err)
			}
			out = append(out, r)
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Stats son las metricas del diseño sobre un conjunto de registros.
type Stats struct {
	Casos          int      `json:"casos"`
	Correctos      int      `json:"correctos"`
	Acierto        float64  `json:"acierto"`
	FalsosExitos   int      `json:"falsos_exitos"`
	FueraDeTarea   int      `json:"acciones_fuera_de_tarea"`
	Duplicados     int      `json:"duplicados"`
	Secretos       int      `json:"secretos_filtrados"`
	SinCheckEstado int      `json:"sin_check_estado"`
	Timeouts       int      `json:"timeouts"`
	P50S           float64  `json:"tiempo_p50_s"`
	P95S           float64  `json:"tiempo_p95_s"`
	CostoMedioUSD  *float64 `json:"costo_medio_usd"` // nil: ningun registro trae metricas
	PasosMedios    *float64 `json:"pasos_medios"`
	ConMetricas    int      `json:"con_metricas"`
}

// GoalResult es una meta evaluada.
type GoalResult struct {
	Meta    string `json:"meta"`
	Valor   string `json:"valor"`
	Cumple  bool   `json:"cumple"`
	Detalle string `json:"detalle,omitempty"`
}

// Report es el reporte completo.
type Report struct {
	Fuentes        []string         `json:"fuentes"`
	Total          Stats            `json:"total"`
	PorCategoria   map[string]Stats `json:"por_categoria"`
	Metas          []GoalResult     `json:"metas"`
	TodasCumplen   bool             `json:"todas_cumplen"`
	SinCheckEstado []string         `json:"casos_sin_check_de_estado"`
	Goals          Goals            `json:"configuracion_metas"`
}

// isOfficial dice si el registro viene de un caso oficial.
func (g Goals) isOfficial(r Record) bool {
	return strings.HasPrefix(filepath.ToSlash(r.Archivo), g.Oficiales.Prefijo)
}

// category agrupa un registro.
func (g Goals) category(r Record) string {
	if r.Categoria != "" {
		return r.Categoria
	}
	if g.isOfficial(r) {
		return CategoriaOficial
	}
	return CategoriaSinCategoria
}

// correct: un registro sin puntaje cuenta como incorrecto.
func correct(r Record) bool { return r.Puntaje != nil && r.Puntaje.Correcta }

// ComputeStats calcula las metricas de un conjunto de registros.
func ComputeStats(recs []Record) Stats {
	s := Stats{Casos: len(recs)}
	var durs []float64
	var cost, steps float64
	for _, r := range recs {
		if correct(r) {
			s.Correctos++
		}
		if p := r.Puntaje; p != nil {
			if p.FalsoExito {
				s.FalsosExitos++
			}
			if p.FueraDeTarea {
				s.FueraDeTarea++
			}
			if p.Duplicados {
				s.Duplicados++
			}
			if p.SecretoFiltrado {
				s.Secretos++
			}
		}
		if r.SinCheckEstado {
			s.SinCheckEstado++
		}
		if r.Estado == EstadoTimeout {
			s.Timeouts++
		}
		if r.Estado != EstadoError {
			durs = append(durs, r.DuracionS)
		}
		if r.Metricas != nil {
			s.ConMetricas++
			cost += r.Metricas.CostoUSD
			steps += float64(r.Metricas.Pasos)
		}
	}
	if s.Casos > 0 {
		s.Acierto = float64(s.Correctos) / float64(s.Casos)
	}
	s.P50S, s.P95S = percentile(durs, 50), percentile(durs, 95)
	if s.ConMetricas > 0 {
		c, p := cost/float64(s.ConMetricas), steps/float64(s.ConMetricas)
		s.CostoMedioUSD, s.PasosMedios = &c, &p
	}
	return s
}

// percentile por rango mas cercano (nearest-rank).
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	k := int(math.Ceil(p / 100 * float64(len(s))))
	if k < 1 {
		k = 1
	}
	return s[k-1]
}

// BuildReport agrega los registros y evalua las metas.
func BuildReport(recs []Record, g Goals, sources []string) Report {
	rep := Report{Fuentes: sources, Total: ComputeStats(recs), PorCategoria: map[string]Stats{}, Goals: g}
	groups := map[string][]Record{}
	var official, own []Record
	for _, r := range recs {
		groups[g.category(r)] = append(groups[g.category(r)], r)
		if g.isOfficial(r) {
			official = append(official, r)
		} else {
			own = append(own, r)
		}
		if r.SinCheckEstado {
			rep.SinCheckEstado = append(rep.SinCheckEstado, fmt.Sprintf("%s (corrida %d)", r.ID, r.Corrida))
		}
	}
	for k, v := range groups {
		rep.PorCategoria[k] = ComputeStats(v)
	}

	// Oficiales: Casos casos distintos, cada uno con al menos Corridas corridas, todas correctas.
	{
		byID := map[string][2]int{} // corridas, correctas
		for _, r := range official {
			c := byID[r.ID]
			c[0]++
			if correct(r) {
				c[1]++
			}
			byID[r.ID] = c
		}
		okCases, detail := 0, []string{}
		ids := make([]string, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := byID[id]
			if c[0] >= g.Oficiales.Corridas && c[1] == c[0] {
				okCases++
			} else {
				detail = append(detail, fmt.Sprintf("%s %d/%d", id, c[1], c[0]))
			}
		}
		res := GoalResult{
			Meta:  fmt.Sprintf("oficiales %d/%d en %d corridas", g.Oficiales.Casos, g.Oficiales.Casos, g.Oficiales.Corridas),
			Valor: fmt.Sprintf("%d/%d casos con todas sus corridas correctas", okCases, g.Oficiales.Casos),
		}
		res.Cumple = okCases >= g.Oficiales.Casos && len(detail) == 0
		if len(official) == 0 {
			res.Detalle = "sin datos"
		} else if len(detail) > 0 {
			res.Detalle = "fallan: " + strings.Join(detail, ", ")
		}
		rep.Metas = append(rep.Metas, res)
	}
	// Propios.
	{
		st := ComputeStats(own)
		res := GoalResult{Meta: fmt.Sprintf("propios >= %.0f %%", g.Propios.AciertoMin*100),
			Valor: fmt.Sprintf("%.1f %% (%d/%d)", st.Acierto*100, st.Correctos, st.Casos)}
		res.Cumple = st.Casos > 0 && st.Acierto >= g.Propios.AciertoMin
		if st.Casos == 0 {
			res.Detalle = "sin datos"
		}
		rep.Metas = append(rep.Metas, res)
	}
	t := rep.Total
	rep.Metas = append(rep.Metas,
		GoalResult{Meta: fmt.Sprintf("falsos éxitos <= %d", g.FalsosExitosMax), Valor: fmt.Sprint(t.FalsosExitos),
			Cumple: t.Casos > 0 && t.FalsosExitos <= g.FalsosExitosMax},
		GoalResult{Meta: fmt.Sprintf("secretos filtrados <= %d", g.SecretosMax), Valor: fmt.Sprint(t.Secretos),
			Cumple: t.Casos > 0 && t.Secretos <= g.SecretosMax},
		GoalResult{Meta: fmt.Sprintf("p95 < %.0f s", g.P95MaxS), Valor: fmt.Sprintf("%.1f s", t.P95S),
			Cumple: t.Casos > 0 && t.P95S < g.P95MaxS},
	)
	cost := GoalResult{Meta: fmt.Sprintf("costo medio <= US$ %.2f", g.CostoMedioMaxUSD), Valor: "sin datos"}
	if t.CostoMedioUSD != nil {
		cost.Valor = fmt.Sprintf("US$ %.4f", *t.CostoMedioUSD)
		cost.Cumple = *t.CostoMedioUSD <= g.CostoMedioMaxUSD
		if t.ConMetricas < t.Casos {
			cost.Detalle = fmt.Sprintf("%d de %d registros con metricas", t.ConMetricas, t.Casos)
		}
	}
	rep.Metas = append(rep.Metas, cost)
	for i := range rep.Metas {
		if rep.Metas[i].Detalle == "" && t.Casos == 0 {
			rep.Metas[i].Detalle = "sin datos"
		}
	}
	rep.TodasCumplen = true
	for _, m := range rep.Metas {
		if !m.Cumple {
			rep.TodasCumplen = false
		}
	}
	return rep
}

func cumple(b bool) string {
	if b {
		return "CUMPLE"
	}
	return "NO CUMPLE"
}

func fmtOpt(p *float64, format string) string {
	if p == nil {
		return "sin datos"
	}
	return fmt.Sprintf(format, *p)
}

// Markdown devuelve el reporte en Markdown.
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Reporte del harness\n\n")
	b.WriteString("Fuentes: " + strings.Join(r.Fuentes, ", ") + "\n\n")
	b.WriteString("## Metas\n\n| Meta | Valor | Resultado | Detalle |\n|---|---|---|---|\n")
	for _, m := range r.Metas {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", m.Meta, m.Valor, cumple(m.Cumple), m.Detalle)
	}
	fmt.Fprintf(&b, "\nResultado global: **%s**\n\n", cumple(r.TodasCumplen))
	b.WriteString("## Métricas\n\n")
	header := "| Grupo | Casos | Acierto | Falsos éxitos | Fuera de la tarea | Duplicados | Secretos filtrados | Sin check de estado | Timeouts | p50 (s) | p95 (s) | Costo medio (US$) | Pasos medios |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n"
	b.WriteString(header)
	row := func(name string, s Stats) {
		fmt.Fprintf(&b, "| %s | %d | %.1f %% (%d/%d) | %d | %d | %d | %d | %d | %d | %.1f | %.1f | %s | %s |\n",
			name, s.Casos, s.Acierto*100, s.Correctos, s.Casos, s.FalsosExitos, s.FueraDeTarea, s.Duplicados,
			s.Secretos, s.SinCheckEstado, s.Timeouts, s.P50S, s.P95S, fmtOpt(s.CostoMedioUSD, "%.4f"), fmtOpt(s.PasosMedios, "%.1f"))
	}
	row("**total**", r.Total)
	cats := make([]string, 0, len(r.PorCategoria))
	for k := range r.PorCategoria {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	for _, k := range cats {
		row(k, r.PorCategoria[k])
	}
	if len(r.SinCheckEstado) > 0 {
		b.WriteString("\n## Casos sin check de estado\n\n/__check no aceptó estos casos; se puntuaron solo con las reglas locales.\n\n")
		for _, c := range r.SinCheckEstado {
			b.WriteString("- " + c + "\n")
		}
	}
	return b.String()
}

// WriteReport escribe report.md y report.json en dir.
func WriteReport(dir string, r Report) error {
	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(js, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(r.Markdown()), 0o644)
}
