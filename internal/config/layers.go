package config

// Lectura de capas YAML y fusion. Mapas se fusionan en profundidad; listas y
// escalares de la capa reemplazan por completo a los de la base (docs/config.md).

import (
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ReadLayer lee un archivo YAML como arbol de mapas, listas y escalares.
// Enteros quedan como int64, decimales como float64. Claves duplicadas, claves
// no textuales y merge keys (<<) son error.
func ReadLayer(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la capa %q: %w", path, err)
	}
	tree, err := ParseLayer(raw)
	if err != nil {
		return nil, fmt.Errorf("capa %q: %w", path, err)
	}
	return tree, nil
}

// ParseLayer interpreta el contenido YAML de una capa.
func ParseLayer(raw []byte) (map[string]any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("YAML invalido: %w", err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return map[string]any{}, nil
	}
	v, err := convert(doc.Content[0], "")
	if err != nil {
		return nil, err
	}
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("la raiz debe ser un mapa")
	}
	return m, nil
}

func convert(n *yaml.Node, path string) (any, error) {
	where := func() string {
		if path == "" {
			return fmt.Sprintf("linea %d", n.Line)
		}
		return fmt.Sprintf("%s (linea %d)", path, n.Line)
	}
	switch n.Kind {
	case yaml.AliasNode:
		return convert(n.Alias, path)
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, val := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
				if k.ShortTag() == "!!merge" {
					return nil, fmt.Errorf("%s: merge keys (<<) no soportadas", where())
				}
				return nil, fmt.Errorf("%s: la clave %q debe ser texto", where(), k.Value)
			}
			key := k.Value
			if _, dup := out[key]; dup {
				return nil, fmt.Errorf("%s: clave duplicada %q", where(), key)
			}
			cv, err := convert(val, joinPath(path, key))
			if err != nil {
				return nil, err
			}
			out[key] = cv
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			cv, err := convert(c, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, cv)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, fmt.Errorf("%s: %w", where(), err)
			}
			return b, nil
		case "!!int":
			var i int64
			if err := n.Decode(&i); err != nil {
				return nil, fmt.Errorf("%s: %w", where(), err)
			}
			return i, nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, fmt.Errorf("%s: %w", where(), err)
			}
			return f, nil
		default: // !!str, !!timestamp, !!binary: se conservan como texto literal
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("%s: nodo YAML no soportado", where())
}

// Merge fusiona layer sobre base y devuelve un arbol nuevo; no modifica sus argumentos.
// Mapas se fusionan en profundidad; listas y escalares de layer reemplazan.
func Merge(base, layer map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(layer))
	for k, v := range base {
		out[k] = clone(v)
	}
	for k, v := range layer {
		bm, bok := out[k].(map[string]any)
		lm, lok := v.(map[string]any)
		if bok && lok {
			out[k] = Merge(bm, lm)
			continue
		}
		out[k] = clone(v)
	}
	return out
}

func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, w := range x {
			out[k] = clone(w)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, w := range x {
			out[i] = clone(w)
		}
		return out
	}
	return v
}

// lookup busca una ruta con puntos ("a.b.c") en un arbol. Los indices de lista
// ("x[0]") cortan la busqueda en la clave que los contiene.
func lookup(tree map[string]any, path string) (any, bool) {
	if path == "" {
		return tree, true
	}
	var cur any = tree
	for _, seg := range strings.Split(path, ".") {
		if i := strings.IndexByte(seg, '['); i >= 0 {
			seg = seg[:i]
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
