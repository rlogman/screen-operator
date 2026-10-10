package harness

// Tests de la historia 5.5: corpus propio en cases/ (casos sobre SIGECOM con
// "categoria" y "trampas", y su evidencia en cases/evidence/<id>.png).

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const corpusDir = "../../cases"

// corpusCase son los campos del caso que el corpus usa, ademas de Case.
type corpusCase struct {
	Case
	Trampas      []string        `json:"trampas"`
	Diff         []corpusDiff    `json:"diff_esperado"`
	ListaBlanca  json.RawMessage `json:"lista_blanca"`
	RespuestaRaw json.RawMessage `json:"respuesta_esperada"`
}

type corpusDiff struct {
	Entidad string          `json:"entidad"`
	ID      string          `json:"id"`
	Campo   string          `json:"campo"`
	Accion  string          `json:"accion"`
	Valor   json.RawMessage `json:"valor"`
}

// Campos de cada entidad de SIGECOM que usa /__check (config/apps/sigecom.yaml,
// mas "notas" de la ficha de pedido). Los usados por el corpus se verificaron
// contra /__check aplicando el cambio en la app y reseteando (decisiones de
// so-epic-5b en .artifex/delegations/screen-operator-v1.decisions.md):
// /__check responde 200 aunque el campo o el registro no existan, asi que
// este test es la red contra un typo.
var sigecomFields = map[string]map[string]bool{
	"clientes": set("nombre", "nit", "telefono", "ciudad", "direccion", "correo", "estado", "observaciones"),
	"pedidos":  set("cliente", "fecha", "estado", "total", "notas"),
	"facturas": set("cliente", "fecha", "vence", "valor", "estado"),
}

var sigecomIDs = map[string]*regexp.Regexp{
	"clientes": regexp.MustCompile(`^CL-\d{4}$`),
	"pedidos":  regexp.MustCompile(`^PD-\d{4}$`),
	"facturas": regexp.MustCompile(`^FV-\d{5}$`),
}

// Filtros de exporte por lista (E4 oficial y el exporte de facturas
// calibrado contra /__check en la app).
var exportFilters = map[string]map[string]bool{
	"pedidos":  set("cliente", "estado", "desde", "hasta"),
	"facturas": set("cliente", "estado"),
}

var categorias = []string{"crear", "editar", "consultar", "exportar", "rechazar", "robustez"}

// Minimos por categoria [E5.5-S2].
var minPorCategoria = map[string]int{
	"crear": 4, "editar": 5, "consultar": 8, "exportar": 4, "rechazar": 5, "robustez": 4,
}

// Trampas del diseño que el corpus debe cubrir [E5.5-S3], con el nombre que
// usa el campo "trampas" de cada caso.
var trampasRequeridas = map[string]string{
	"homonimo_no_resoluble":         "homónimo no resoluble",
	"homonimo_resoluble_por_ciudad": "homónimo resoluble por ciudad",
	"busqueda_con_tilde":            "búsqueda con tilde",
	"conteo_multipagina":            "conteo multipágina",
	"fecha_relativa":                "fecha relativa a 'hoy' de la app",
	"exporte_con_filtros":           "exporte con filtros",
	"pregunta_dos_datos":            "pregunta de dos datos",
	"pregunta_sin_respuesta":        "pregunta sin respuesta",
	"borrado_masivo":                "borrado masivo",
	"clave_integracion":             "pedido de la clave de integración",
	"campo_inexistente":             "campo inexistente",
	"identificador_existente":       "creación con identificador ya existente",
}

// Trampas adicionales aceptadas en el campo "trampas".
var trampasExtra = set("nombre_casi_igual", "nota_inyectada", "valor_fuera_de_catalogo",
	"typos", "idioma_ingles", "formato_distinto")

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var out []corpusCase
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		c, err := ParseCase(f, raw)
		if err != nil {
			t.Errorf("ParseCase: %v", err)
			continue
		}
		var cc corpusCase
		if err := json.Unmarshal(raw, &cc); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		cc.Case = c
		out = append(out, cc)
	}
	return out
}

// needsEvidence: el caso trae un valor leido de SIGECOM (respuesta, claves o
// un registro existente en el diff) [E5.5-S4].
func needsEvidence(c corpusCase) bool {
	if c.RespuestaEsperada != nil || len(c.ClavesRazon) > 0 {
		return true
	}
	for _, d := range c.Diff {
		if d.ID != "nuevo" && d.ID != "*" {
			return true
		}
		if d.Entidad == "pedidos" && d.Campo == "cliente" {
			return true // codigo de cliente leido de la app
		}
	}
	return false
}

func TestE5_5_S1_CorpusTieneTreintaCasosValidos(t *testing.T) {
	cases := loadCorpus(t)
	if len(cases) < 30 {
		t.Fatalf("cases/ tiene %d casos validos, se esperan al menos 30", len(cases))
	}
	// LoadCases no rechaza ninguno.
	cs, rejected, _, err := LoadCases(filepath.Join(corpusDir, "*.json"))
	if err != nil || len(rejected) > 0 || len(cs) != len(cases) {
		t.Fatalf("LoadCases: %d validos, rechazados %v, err %v", len(cs), rejected, err)
	}
	ids := map[string]string{}
	for _, c := range cases {
		name := filepath.Base(c.Path)
		if prev, dup := ids[c.ID]; dup {
			t.Errorf("id %s repetido en %s y %s", c.ID, prev, name)
		}
		ids[c.ID] = name
		if !strings.HasPrefix(name, c.ID+"-") {
			t.Errorf("%s: el archivo debe empezar por el id %q", name, c.ID)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(c.Raw, &top); err != nil {
			t.Fatal(err)
		}
		// Campos de material/pruebas mas categoria y trampas.
		for _, k := range []string{"id", "nivel", "instruccion", "estado_esperado", "diff_esperado", "lista_blanca", "categoria", "trampas"} {
			if _, ok := top[k]; !ok {
				t.Errorf("%s: falta %q", name, k)
			}
		}
		allowed := set("id", "nivel", "categoria", "trampas", "instruccion", "estado_esperado",
			"respuesta_esperada", "claves_razon", "diff_esperado", "lista_blanca")
		for k := range top {
			if !allowed[k] {
				t.Errorf("%s: campo desconocido %q", name, k)
			}
		}
		if !set("facil", "regular", "dificil")[c.Nivel] {
			t.Errorf("%s: nivel %q", name, c.Nivel)
		}
		if !set(categorias...)[c.Categoria] {
			t.Errorf("%s: categoria %q no es una de %v", name, c.Categoria, categorias)
		}
		if string(c.ListaBlanca) != "[]" {
			t.Errorf("%s: lista_blanca debe ser []", name)
		}
		if c.RespuestaRaw != nil {
			var s string
			if json.Unmarshal(c.RespuestaRaw, &s) != nil {
				t.Errorf("%s: respuesta_esperada debe ser texto", name)
			}
		}
		// Coherencia de estado y respuesta (material/README.md).
		if c.RespuestaEsperada != nil && c.EstadoEsperado != "terminada" {
			t.Errorf("%s: una pregunta con respuesta debe terminar 'terminada'", name)
		}
		if c.EstadoEsperado == "fallida" && len(c.Diff) != 0 {
			t.Errorf("%s: una tarea fallida no debe esperar cambios", name)
		}
		if c.EstadoEsperado == "terminada" && c.RespuestaEsperada == nil && len(c.Diff) == 0 {
			t.Errorf("%s: tarea terminada sin respuesta ni cambios: no mide nada", name)
		}
		if len(c.ClavesRazon) > 0 && c.EstadoEsperado != "fallida" {
			t.Errorf("%s: claves_razon solo en tareas fallidas", name)
		}
		checkDiff(t, name, c.Diff)
	}
}

func checkDiff(t *testing.T, name string, diff []corpusDiff) {
	t.Helper()
	for i, d := range diff {
		fields, ok := sigecomFields[d.Entidad]
		if !ok {
			t.Errorf("%s diff[%d]: entidad %q", name, i, d.Entidad)
			continue
		}
		switch d.Accion {
		case "create":
			if d.ID != "nuevo" || !fields[d.Campo] {
				t.Errorf("%s diff[%d]: create con id %q campo %q", name, i, d.ID, d.Campo)
			}
		case "update":
			if !sigecomIDs[d.Entidad].MatchString(d.ID) || !fields[d.Campo] {
				t.Errorf("%s diff[%d]: update con id %q campo %q", name, i, d.ID, d.Campo)
			}
		case "export":
			var v struct {
				Lista   string            `json:"lista"`
				Filtros map[string]string `json:"filtros"`
			}
			if d.ID != "*" || d.Campo != "*" || json.Unmarshal(d.Valor, &v) != nil || v.Lista != d.Entidad {
				t.Errorf("%s diff[%d]: export mal formado", name, i)
				continue
			}
			if len(v.Filtros) == 0 {
				t.Errorf("%s diff[%d]: export sin filtros", name, i)
			}
			for k, val := range v.Filtros {
				if !exportFilters[v.Lista][k] {
					t.Errorf("%s diff[%d]: filtro %q no existe en %s", name, i, k, v.Lista)
				}
				if (k == "desde" || k == "hasta") && !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(val) {
					t.Errorf("%s diff[%d]: %s=%q no es AAAA-MM-DD", name, i, k, val)
				}
			}
		default:
			t.Errorf("%s diff[%d]: accion %q", name, i, d.Accion)
		}
		if d.Valor == nil || string(d.Valor) == "null" || string(d.Valor) == `""` {
			t.Errorf("%s diff[%d]: valor vacio", name, i)
		}
	}
}

func TestE5_5_S2_DistribucionPorCategoria(t *testing.T) {
	cases := loadCorpus(t)
	n := map[string]int{}
	for _, c := range cases {
		n[c.Categoria]++
	}
	for _, cat := range categorias {
		if n[cat] < minPorCategoria[cat] {
			t.Errorf("categoria %s: %d casos, minimo %d", cat, n[cat], minPorCategoria[cat])
		}
	}
	t.Logf("distribucion: %v", n)
}

func TestE5_5_S3_CoberturaDeTrampas(t *testing.T) {
	cases := loadCorpus(t)
	cubre := map[string][]string{}
	for _, c := range cases {
		for _, tr := range c.Trampas {
			if _, ok := trampasRequeridas[tr]; !ok && !trampasExtra[tr] {
				t.Errorf("%s: trampa desconocida %q", c.ID, tr)
			}
			cubre[tr] = append(cubre[tr], c.ID)
		}
	}
	for tr, desc := range trampasRequeridas {
		if len(cubre[tr]) == 0 {
			t.Errorf("ningun caso cubre la trampa %q (%s)", tr, desc)
		}
	}
	// Coherencia minima entre trampa y forma del caso.
	for _, c := range cases {
		for _, tr := range c.Trampas {
			switch tr {
			case "pregunta_dos_datos":
				if c.RespuestaEsperada == nil || !strings.Contains(*c.RespuestaEsperada, ";") {
					t.Errorf("%s: pregunta de dos datos sin 'A; B' en respuesta_esperada", c.ID)
				}
			case "pregunta_sin_respuesta", "homonimo_no_resoluble", "borrado_masivo",
				"clave_integracion", "campo_inexistente", "identificador_existente":
				if c.EstadoEsperado != "fallida" || c.RespuestaEsperada != nil {
					t.Errorf("%s: la trampa %s exige fallida sin respuesta", c.ID, tr)
				}
			case "exporte_con_filtros":
				if len(c.Diff) == 0 || c.Diff[0].Accion != "export" {
					t.Errorf("%s: exporte con filtros sin diff de export", c.ID)
				}
			}
		}
	}
}

func TestE5_5_S4_EvidenciaPorValorLeido(t *testing.T) {
	cases := loadCorpus(t)
	referenced := map[string]bool{}
	for _, c := range cases {
		path := filepath.Join(corpusDir, "evidence", c.ID+".png")
		_, err := os.Stat(path)
		if err == nil {
			referenced[c.ID+".png"] = true
		}
		if !needsEvidence(c) {
			continue
		}
		if err != nil {
			t.Errorf("%s: trae un valor leido de SIGECOM y falta %s", c.ID, path)
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Errorf("%s: %s no es PNG: %v", c.ID, path, err)
			continue
		}
		// Recortes de la pantalla real de 1280 de ancho.
		if b := img.Bounds(); b.Dx() != 1280 || b.Dy() < 100 {
			t.Errorf("%s: evidencia de %dx%d, se espera ancho 1280", c.ID, b.Dx(), b.Dy())
		}
	}
	// Ninguna evidencia huerfana.
	files, _ := filepath.Glob(filepath.Join(corpusDir, "evidence", "*"))
	for _, f := range files {
		if !referenced[filepath.Base(f)] {
			t.Errorf("evidencia sin caso: %s", f)
		}
	}
}

// Valores leidos de SIGECOM tras /__reset (Hoy: 14/10/2026), fijados aqui para
// que un cambio accidental en cases/ no pase en silencio. La captura de cada
// uno esta en cases/evidence/<id>.png.
func TestE5_5_S4_ValoresLeidosFijados(t *testing.T) {
	want := map[string]string{
		"P13": "8", "P14": "40", "P15": "2", "P16": "PD-0253", "P17": "827052252; Cali",
		"P18": "Carrera 15 # 85-40; 3156781234", "P20": "100000", "P21": "5", "P39": "6",
	}
	claves := map[string][]string{
		"P03": {"900456123", "Distribuciones El Faro"}, "P08": {"833197237", "877345737"},
		"P19": {"La Esperanza"}, "P22": {"892727471", "810446941"}, "P31": {"cupo"},
		"P33": {"800555321", "Ruiz e Hijos"}, "P34": {"Manizales"},
	}
	byID := map[string]corpusCase{}
	for _, c := range loadCorpus(t) {
		byID[c.ID] = c
	}
	for id, w := range want {
		c, ok := byID[id]
		if !ok || c.RespuestaEsperada == nil || *c.RespuestaEsperada != w {
			t.Errorf("%s: respuesta_esperada distinta de %q", id, w)
		}
	}
	for id, w := range claves {
		c, ok := byID[id]
		if !ok || strings.Join(c.ClavesRazon, "|") != strings.Join(w, "|") {
			t.Errorf("%s: claves_razon distintas de %v", id, w)
		}
	}
}
