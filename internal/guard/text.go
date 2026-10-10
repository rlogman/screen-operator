package guard

import (
	"regexp"
	"strings"
)

var foldMap = map[rune]rune{'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ú': 'u', 'ü': 'u', 'à': 'a', 'è': 'e', 'ì': 'i', 'ò': 'o', 'ù': 'u',
	'Á': 'a', 'É': 'e', 'Í': 'i', 'Ó': 'o', 'Ú': 'u', 'Ü': 'u', 'ñ': 'n', 'Ñ': 'n'}

// fold: minusculas, sin tildes, espacios colapsados. Para comparar palabras
// clave, etiquetas y nombres de campos (no valores).
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 0x300 && r <= 0x36f {
			continue
		}
		if f, ok := foldMap[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// lowerExact: minusculas y espacios colapsados, con tildes (para nombres:
// "Ramírez" y "Ramirez" son registros distintos).
func lowerExact(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// unaccent quita tildes conservando mayusculas y la ñ.
func unaccent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'ñ', 'Ñ':
			b.WriteRune(r)
			continue
		}
		if f, ok := foldMap[r]; ok {
			if strings.ToUpper(string(r)) == string(r) {
				f = []rune(strings.ToUpper(string(f)))[0]
			}
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasAccent(s string) bool { return unaccent(s) != s }

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// reID reconoce identificadores: codigos (CL-0007, RP-12) y numeros largos
// (cedulas, NIT, con o sin puntos y digito de verificacion).
var reID = regexp.MustCompile(`^(?:[A-Za-z]{1,5}-?\d{2,}|\d[\d.\-]{5,}\d)$`)

func isID(s string) bool { return reID.MatchString(strings.TrimSpace(s)) }

// reNumber encuentra numeros en un texto, admitiendo separadores internos.
var reNumber = regexp.MustCompile(`\d[\d.\-]*\d|\d`)

// containsBounded dice si want aparece en s (exacto) sin un caracter
// alfanumerico pegado a cada lado: "Calle 80 # 24-15" no esta en
// "Calle 80 # 24-150".
func containsBounded(s, want string) bool {
	if want == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(s[i:], want)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(want)
		okL := start == 0 || !isAlnum(lastRune(s[:start]))
		okR := end == len(s) || !isAlnum(firstRune(s[end:]))
		if okL && okR {
			return true
		}
		i = start + 1
	}
}

func isAlnum(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0
	}
	return rs[len(rs)-1]
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
