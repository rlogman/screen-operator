package config

// Validador de JSON Schema 2020-12 para el subconjunto de palabras clave que usa
// config/schema.json. Compilar un esquema con una palabra clave desconocida es error:
// asi el validador nunca ignora en silencio una regla que alguien agregue al esquema.

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ValidationError es una violacion del esquema en una ruta de la configuracion.
type ValidationError struct {
	Path string // ruta con puntos, p. ej. "surface.input.clik_hold_ms"; indices como "[0]"
	Msg  string
}

func (e ValidationError) Error() string {
	p := e.Path
	if p == "" {
		p = "(raiz)"
	}
	return p + ": " + e.Msg
}

// Schema es un JSON Schema compilado.
type Schema struct {
	root     map[string]any
	patterns map[string]*regexp.Regexp
}

// palabras clave que este validador aplica o que son solo anotaciones.
var knownKeywords = map[string]bool{
	"$schema": true, "$id": true, "$ref": true, "$defs": true, "$comment": true,
	"title": true, "description": true, "default": true, "examples": true,
	"format": true, // anotacion, igual que Draft202012Validator sin format_checker
	"type":   true, "properties": true, "additionalProperties": true, "required": true,
	"enum": true, "const": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "minLength": true, "maxLength": true,
	"pattern": true, "items": true, "minItems": true, "maxItems": true, "propertyNames": true,
}

// CompileSchema parsea y verifica un JSON Schema.
func CompileSchema(raw []byte) (*Schema, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("esquema: JSON invalido: %w", err)
	}
	root, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("esquema: la raiz debe ser un objeto")
	}
	s := &Schema{root: root, patterns: map[string]*regexp.Regexp{}}
	if err := s.check(root, "#"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Schema) check(node any, at string) error {
	if _, ok := node.(bool); ok {
		return nil
	}
	m, ok := node.(map[string]any)
	if !ok {
		return fmt.Errorf("esquema: %s no es un esquema", at)
	}
	for k, v := range m {
		if !knownKeywords[k] {
			return fmt.Errorf("esquema: palabra clave no soportada %q en %s", k, at)
		}
		switch k {
		case "$ref":
			ref, _ := v.(string)
			if _, err := s.resolve(ref); err != nil {
				return fmt.Errorf("esquema: %s: %w", at, err)
			}
		case "$defs", "properties":
			sub, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("esquema: %s/%s debe ser un objeto", at, k)
			}
			for name, child := range sub {
				if err := s.check(child, at+"/"+k+"/"+name); err != nil {
					return err
				}
			}
		case "additionalProperties", "items", "propertyNames":
			if err := s.check(v, at+"/"+k); err != nil {
				return err
			}
		case "pattern":
			p, ok := v.(string)
			if !ok {
				return fmt.Errorf("esquema: %s/pattern debe ser texto", at)
			}
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("esquema: %s/pattern: %w", at, err)
			}
			s.patterns[p] = re
		case "type":
			for _, t := range typeList(v) {
				switch t {
				case "object", "array", "string", "number", "integer", "boolean", "null":
				default:
					return fmt.Errorf("esquema: %s/type desconocido %q", at, t)
				}
			}
		case "required", "enum":
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("esquema: %s/%s debe ser una lista", at, k)
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
			"minLength", "maxLength", "minItems", "maxItems":
			if _, ok := v.(float64); !ok {
				return fmt.Errorf("esquema: %s/%s debe ser un numero", at, k)
			}
		}
	}
	return nil
}

func (s *Schema) resolve(ref string) (any, error) {
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("$ref %q no soportado (solo #/$defs/<nombre>)", ref)
	}
	defs, _ := s.root["$defs"].(map[string]any)
	d, ok := defs[strings.TrimPrefix(ref, prefix)]
	if !ok {
		return nil, fmt.Errorf("$ref %q no existe", ref)
	}
	return d, nil
}

func typeList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			s, _ := x.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}

// Validate devuelve todas las violaciones del valor, ordenadas por ruta.
func (s *Schema) Validate(v any) []ValidationError {
	var errs []ValidationError
	s.validate(s.root, v, "", &errs)
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Path < errs[j].Path })
	return errs
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func (s *Schema) validate(node any, v any, path string, errs *[]ValidationError) {
	add := func(p, format string, args ...any) {
		*errs = append(*errs, ValidationError{Path: p, Msg: fmt.Sprintf(format, args...)})
	}
	if b, ok := node.(bool); ok {
		if !b {
			add(path, "no se admite ningun valor aqui")
		}
		return
	}
	m := node.(map[string]any)

	if ref, ok := m["$ref"].(string); ok {
		target, _ := s.resolve(ref)
		s.validate(target, v, path, errs)
	}
	if t, ok := m["type"]; ok {
		types := typeList(t)
		match := false
		for _, ty := range types {
			if hasType(v, ty) {
				match = true
				break
			}
		}
		if !match {
			add(path, "tipo %s, se esperaba %s", describe(v), strings.Join(types, " o "))
			return
		}
	}
	if c, ok := m["const"]; ok && !equal(c, v) {
		add(path, "valor %s, se esperaba exactamente %s", show(v), show(c))
	}
	if e, ok := m["enum"].([]any); ok {
		found := false
		for _, x := range e {
			if equal(x, v) {
				found = true
				break
			}
		}
		if !found {
			opts := make([]string, len(e))
			for i, x := range e {
				opts[i] = show(x)
			}
			add(path, "valor %s no permitido; opciones: %s", show(v), strings.Join(opts, ", "))
		}
	}
	if n, ok := number(v); ok {
		if lim, ok := m["minimum"].(float64); ok && n < lim {
			add(path, "%s es menor que el minimo %s", show(v), show(lim))
		}
		if lim, ok := m["maximum"].(float64); ok && n > lim {
			add(path, "%s es mayor que el maximo %s", show(v), show(lim))
		}
		if lim, ok := m["exclusiveMinimum"].(float64); ok && n <= lim {
			add(path, "%s debe ser mayor que %s", show(v), show(lim))
		}
		if lim, ok := m["exclusiveMaximum"].(float64); ok && n >= lim {
			add(path, "%s debe ser menor que %s", show(v), show(lim))
		}
	}
	if str, ok := v.(string); ok {
		l := float64(utf8.RuneCountInString(str))
		if lim, ok := m["minLength"].(float64); ok && l < lim {
			add(path, "texto de %d caracteres, minimo %s", int(l), show(lim))
		}
		if lim, ok := m["maxLength"].(float64); ok && l > lim {
			add(path, "texto de %d caracteres, maximo %s", int(l), show(lim))
		}
		if p, ok := m["pattern"].(string); ok && !s.patterns[p].MatchString(str) {
			add(path, "%s no cumple el patron %s", show(v), p)
		}
	}
	if obj, ok := v.(map[string]any); ok {
		keys := sortedKeys(obj)
		if req, ok := m["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, present := obj[name]; !present {
					add(joinPath(path, name), "falta la clave requerida")
				}
			}
		}
		if pn, ok := m["propertyNames"]; ok {
			for _, k := range keys {
				var sub []ValidationError
				s.validate(pn, k, joinPath(path, k), &sub)
				if len(sub) > 0 {
					add(joinPath(path, k), "nombre de clave no permitido: %s", sub[0].Msg)
				}
			}
		}
		props, _ := m["properties"].(map[string]any)
		addl, hasAddl := m["additionalProperties"]
		for _, k := range keys {
			p := joinPath(path, k)
			if ps, ok := props[k]; ok {
				s.validate(ps, obj[k], p, errs)
				continue
			}
			if !hasAddl {
				continue
			}
			if b, ok := addl.(bool); ok {
				if !b {
					add(p, "clave desconocida (no esta en config/schema.json)")
				}
				continue
			}
			s.validate(addl, obj[k], p, errs)
		}
	}
	if arr, ok := v.([]any); ok {
		if lim, ok := m["minItems"].(float64); ok && float64(len(arr)) < lim {
			add(path, "lista de %d elementos, minimo %s", len(arr), show(lim))
		}
		if lim, ok := m["maxItems"].(float64); ok && float64(len(arr)) > lim {
			add(path, "lista de %d elementos, maximo %s", len(arr), show(lim))
		}
		if it, ok := m["items"]; ok {
			for i, x := range arr {
				s.validate(it, x, fmt.Sprintf("%s[%d]", path, i), errs)
			}
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func hasType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := number(v)
		return ok
	case "integer":
		n, ok := number(v)
		return ok && !math.IsInf(n, 0) && n == math.Trunc(n)
	}
	return false
}

func describe(v any) string {
	switch v.(type) {
	case map[string]any:
		return "objeto"
	case []any:
		return "lista"
	case string:
		return "texto"
	case bool:
		return "booleano"
	case nil:
		return "nulo"
	case int, int64:
		return "entero"
	case float64:
		return "numero"
	}
	return fmt.Sprintf("%T", v)
}

func show(v any) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case nil:
		return "null"
	}
	return fmt.Sprintf("%v", v)
}

// equal compara valores tipo JSON; los numeros se comparan por valor.
func equal(a, b any) bool {
	if na, ok := number(a); ok {
		nb, ok := number(b)
		return ok && na == nb
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !equal(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equal(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}
