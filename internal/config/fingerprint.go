package config

// Huella de app (epica 10, incidente FR-0153): ademas del texto visible, el
// perfil puede declarar la version que espera ver (app.match.version). Si la
// pantalla muestra otra version, el perfil no se usa y queda registrado el
// cambio de app.

import (
	"fmt"
	"regexp"
	"strings"
)

// AppResolution es el resultado de resolver el perfil de app con su huella:
// texto (app.match.screen_text_any) y version visible (app.match.version).
type AppResolution struct {
	Layer Layer
	// OK indica que el perfil coincide por texto y por version: sus pistas se
	// pueden usar.
	OK bool
	// Expected y Seen son la version esperada por el perfil y la leida en
	// pantalla, normalizadas con "v" adelante (vacias si no aplican).
	Expected, Seen string
	// Change describe un cambio de app detectado ("cambio de app detectado:
	// v3.9 -> v4.2"): el texto coincide pero la version visible no.
	Change string
}

// defaultVersionPattern captura una version visible del tipo "v4.2",
// "V 3.9.1" o "version 4.2" (con o sin tilde). Exige al menos un punto para no
// confundir fechas ni codigos.
var defaultVersionPattern = regexp.MustCompile(`(?i)(?:\bv|\bversi[oó]n)\s*(\d+(?:\.\d+)+)\b`)

// VisibleVersion extrae la version visible del texto de pantalla con pattern
// (vacio = patron por defecto; si el patron tiene grupos, el primero es la
// version). Devuelve "" si no hay version visible.
func VisibleVersion(screenText, pattern string) (string, error) {
	re := defaultVersionPattern
	if pattern != "" {
		var err error
		if re, err = regexp.Compile(pattern); err != nil {
			return "", fmt.Errorf("app.match.version_pattern invalido: %w", err)
		}
	}
	m := re.FindStringSubmatch(screenText)
	switch {
	case m == nil:
		return "", nil
	case len(m) > 1:
		return NormVersion(m[1]), nil
	default:
		return NormVersion(m[0]), nil
	}
}

// NormVersion normaliza una version a la forma "v4.2" ("" si esta vacia).
func NormVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "vV")
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return "v" + v
}

// ResolveAppFingerprint selecciona el perfil por texto (ResolveApp, que sigue
// siendo la seleccion de la epica 1) y ademas compara la version visible con
// app.match.version. Si el perfil declara version y la pantalla muestra otra,
// el perfil NO se usa (OK=false) y Change lo describe: el agente sigue en modo
// generico. Si la pantalla no muestra ninguna version no hay evidencia de
// cambio y el perfil se usa.
func (l *Loader) ResolveAppFingerprint(screenText string) (AppResolution, error) {
	layer, ok, err := l.ResolveApp(screenText)
	if err != nil || !ok {
		return AppResolution{}, err
	}
	res := AppResolution{Layer: layer, OK: true}
	want, _ := lookupString(layer.Tree, "app.match.version")
	if want == "" {
		return res, nil
	}
	pattern, _ := lookupString(layer.Tree, "app.match.version_pattern")
	seen, err := VisibleVersion(screenText, pattern)
	if err != nil {
		return AppResolution{}, err
	}
	res.Expected, res.Seen = NormVersion(want), seen
	if seen != "" && !strings.EqualFold(seen, res.Expected) {
		res.OK = false
		res.Change = fmt.Sprintf("cambio de app detectado: %s -> %s", res.Expected, seen)
	}
	return res, nil
}
