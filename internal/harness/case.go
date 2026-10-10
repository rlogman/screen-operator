package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Case es una prueba en el formato de material/pruebas, mas el campo opcional
// "categoria" de los casos propios. Raw es el JSON tal cual, que se manda a
// POST /__check sin tocarlo.
type Case struct {
	Path              string   `json:"-"`
	Raw               []byte   `json:"-"`
	ID                string   `json:"id"`
	Nivel             string   `json:"nivel,omitempty"`
	Categoria         string   `json:"categoria,omitempty"`
	Instruccion       string   `json:"instruccion"`
	EstadoEsperado    string   `json:"estado_esperado"`
	RespuestaEsperada *string  `json:"-"`
	ClavesRazon       []string `json:"claves_razon,omitempty"`
	// Pregunta marca explicitamente un caso como pregunta (si falta, se infiere
	// de "?" o "¿" en la instruccion).
	Pregunta *bool `json:"pregunta,omitempty"`
}

// IsQuestion dice si el caso es una pregunta.
func (c Case) IsQuestion() bool {
	if c.Pregunta != nil {
		return *c.Pregunta
	}
	if c.RespuestaEsperada != nil {
		return true
	}
	return strings.ContainsAny(c.Instruccion, "?¿")
}

// ParseCase interpreta el JSON de una prueba y lo rechaza si le falta algo
// que la puntuacion necesita:
//   - "instruccion" no vacia;
//   - "estado_esperado" igual a "terminada" o "fallida";
//   - "diff_esperado" presente y arreglo (puede ser []). /__check trata un diff
//     ausente como vacio y devuelve pass=true, asi que un caso sin el campo
//     contaria como acierto sin verificar nada (docs/assumptions.md).
func ParseCase(path string, raw []byte) (Case, error) {
	var c Case
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&c); err != nil {
		return Case{}, fmt.Errorf("%s: JSON invalido: %w", path, err)
	}
	var extra map[string]any
	d2 := json.NewDecoder(bytes.NewReader(raw))
	d2.UseNumber()
	if err := d2.Decode(&extra); err != nil {
		return Case{}, fmt.Errorf("%s: %w", path, err)
	}
	switch v := extra["respuesta_esperada"].(type) {
	case nil:
	case string:
		c.RespuestaEsperada = &v
	case json.Number:
		s := v.String()
		c.RespuestaEsperada = &s
	default:
		return Case{}, fmt.Errorf("%s: respuesta_esperada debe ser texto o numero", path)
	}
	c.Path, c.Raw = path, raw
	if strings.TrimSpace(c.ID) == "" {
		return Case{}, fmt.Errorf("%s: falta 'id'", path)
	}
	if _, ok := extra["instruccion"].(string); !ok || strings.TrimSpace(c.Instruccion) == "" {
		return Case{}, fmt.Errorf("%s: falta 'instruccion'", path)
	}
	if _, ok := extra["estado_esperado"]; !ok {
		return Case{}, fmt.Errorf("%s: falta 'estado_esperado'", path)
	}
	if c.EstadoEsperado != "terminada" && c.EstadoEsperado != "fallida" {
		return Case{}, fmt.Errorf("%s: estado_esperado debe ser 'terminada' o 'fallida'", path)
	}
	d, ok := extra["diff_esperado"]
	if !ok {
		return Case{}, fmt.Errorf("%s: falta 'diff_esperado' (usar [] si la tarea no cambia nada)", path)
	}
	if _, ok := d.([]any); !ok {
		return Case{}, fmt.Errorf("%s: 'diff_esperado' debe ser un arreglo", path)
	}
	return c, nil
}

// Rejected es un archivo de caso que ParseCase rechazo: no se corre.
type Rejected struct {
	Path   string `json:"archivo"`
	Motivo string `json:"motivo"`
}

// LoadCases expande una lista de patrones glob separados por comas, quita
// duplicados y devuelve los casos en orden alfabetico de ruta. Un patron sin
// coincidencias va en warnings (el llamador lo muestra). Un archivo que
// ParseCase rechaza va en rejected y no se corre; los demas si. Ningun caso
// valido en total es error: un typo no debe correr un corpus vacio en silencio.
func LoadCases(patterns string) (cases []Case, rejected []Rejected, warnings []string, err error) {
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Split(patterns, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m, err := filepath.Glob(p)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("patron %q: %w", p, err)
		}
		if len(m) == 0 {
			warnings = append(warnings, fmt.Sprintf("el patron %q no coincide con ningun archivo", p))
			continue
		}
		for _, f := range m {
			f = filepath.Clean(f)
			if !seen[f] {
				seen[f] = true
				paths = append(paths, f)
			}
		}
	}
	if len(paths) == 0 {
		return nil, nil, warnings, errors.New("no hay casos que correr")
	}
	sort.Strings(paths)
	cases = make([]Case, 0, len(paths))
	for _, f := range paths {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, warnings, err
		}
		c, err := ParseCase(f, raw)
		if err != nil {
			rejected = append(rejected, Rejected{Path: f, Motivo: strings.TrimPrefix(err.Error(), f+": ")})
			continue
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, rejected, warnings, errors.New("no hay casos validos que correr")
	}
	return cases, rejected, warnings, nil
}
