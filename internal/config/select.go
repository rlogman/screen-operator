package config

// Seleccion en runtime: superficie por la URL del escritorio (surface.match) y
// perfil de app por texto visible en la primera captura (app.match.screen_text_any).

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrUnsupportedSurface indica que ninguna superficie coincide con la URL.
var ErrUnsupportedSurface = errors.New("superficie no soportada")

// ResolveSurface elige la superficie cuya surface.match coincide con la URL:
// esquema en url_schemes (sin distinguir mayusculas) y ruta que termina en
// url_path_suffix. Una superficie sin criterios de match nunca coincide.
func (l *Loader) ResolveSurface(rawURL string) (Layer, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Layer{}, fmt.Errorf("%w: URL invalida: %v", ErrUnsupportedSurface, err)
	}
	surfaces, err := l.Surfaces()
	if err != nil {
		return Layer{}, err
	}
	var hits []Layer
	for _, s := range surfaces {
		if surfaceMatches(s.Tree, u) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Layer{}, fmt.Errorf("%w: esquema %q, ruta %q", ErrUnsupportedSurface, u.Scheme, u.Path)
	default:
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		return Layer{}, fmt.Errorf("URL con esquema %q y ruta %q coincide con varias superficies: %s",
			u.Scheme, u.Path, strings.Join(ids, ", "))
	}
}

func surfaceMatches(tree map[string]any, u *url.URL) bool {
	schemes := stringList(tree, "surface.match.url_schemes")
	suffix, _ := lookupString(tree, "surface.match.url_path_suffix")
	if len(schemes) == 0 && suffix == "" {
		return false
	}
	if len(schemes) > 0 {
		ok := false
		for _, s := range schemes {
			if strings.EqualFold(s, u.Scheme) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return suffix == "" || strings.HasSuffix(u.Path, suffix)
}

// AppProfilesDisabled indica si SO_DISABLE_APP_PROFILES apaga los perfiles de app.
// Cualquier valor no vacio distinto de un falso explicito ("0", "false") los apaga.
func (l *Loader) AppProfilesDisabled() bool {
	v := strings.TrimSpace(l.Getenv(EnvDisableAppProfiles))
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	return err != nil || b
}

// ResolveApp elige el perfil de app cuyo app.match.screen_text_any aparece en el
// texto de pantalla (subcadena, sin distinguir mayusculas). Sin coincidencia, con
// varias coincidencias o con SO_DISABLE_APP_PROFILES activo, el perfil es vacio
// (ok=false) y el agente opera como generico.
func (l *Loader) ResolveApp(screenText string) (Layer, bool, error) {
	if l.AppProfilesDisabled() {
		return Layer{}, false, nil
	}
	apps, err := l.AppProfiles()
	if err != nil {
		return Layer{}, false, err
	}
	text := strings.ToLower(screenText)
	var hits []Layer
	for _, a := range apps {
		for _, needle := range stringList(a.Tree, "app.match.screen_text_any") {
			if needle != "" && strings.Contains(text, strings.ToLower(needle)) {
				hits = append(hits, a)
				break
			}
		}
	}
	if len(hits) != 1 {
		return Layer{}, false, nil
	}
	return hits[0], true, nil
}

// ForTask resuelve la configuracion de una tarea: base <- superficie (por URL) <-
// perfil de app (por texto de pantalla, si hay) <- overlay.
func (l *Loader) ForTask(desktopURL, screenText string) (*Config, error) {
	s, err := l.ResolveSurface(desktopURL)
	if err != nil {
		return nil, err
	}
	layers := []string{s.Path}
	if a, ok, err := l.ResolveApp(screenText); err != nil {
		return nil, err
	} else if ok {
		layers = append(layers, a.Path)
	}
	return l.Load(layers...)
}

func lookupString(tree map[string]any, path string) (string, bool) {
	v, ok := lookup(tree, path)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func stringList(tree map[string]any, path string) []string {
	v, _ := lookup(tree, path)
	xs, _ := v.([]any)
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
