package config

// Catalogo de superficies (surfaces/*.yaml) y perfiles de app (apps/*.yaml), y la
// verificacion completa que corre el agente al arrancar.

import (
	"fmt"
	"path/filepath"
	"sort"
)

// Layer es una capa con nombre del catalogo (superficie o perfil de app).
type Layer struct {
	ID   string
	Path string
	Tree map[string]any
}

func (l *Loader) catalog(subdir, root string) ([]Layer, error) {
	files, err := filepath.Glob(filepath.Join(l.Dir, subdir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := make([]Layer, 0, len(files))
	seen := map[string]string{}
	for _, f := range files {
		t, err := ReadLayer(f)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		id, _ := lookup(t, root+".id")
		s, ok := id.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("config: %s: falta %s.id", f, root)
		}
		if prev, dup := seen[s]; dup {
			return nil, fmt.Errorf("config: %s.id %q repetido en %s y %s", root, s, prev, f)
		}
		seen[s] = f
		out = append(out, Layer{ID: s, Path: f, Tree: t})
	}
	return out, nil
}

// Surfaces lista las superficies de surfaces/*.yaml ordenadas por archivo.
func (l *Loader) Surfaces() ([]Layer, error) { return l.catalog(defaultSurfacesSubdir, "surface") }

// AppProfiles lista los perfiles de apps/*.yaml ordenados por archivo.
func (l *Loader) AppProfiles() ([]Layer, error) {
	return l.catalog(defaultAppProfilesSubd, "app")
}

// CheckAll valida todas las combinaciones que el agente puede resolver en runtime:
// base <- cada superficie <- (sin app | cada app) <- overlay. Es la verificacion de
// arranque: una clave desconocida o un valor fuera de rango detiene el proceso.
func (l *Loader) CheckAll() (surfaces, apps int, err error) {
	ss, err := l.Surfaces()
	if err != nil {
		return 0, 0, err
	}
	if len(ss) == 0 {
		return 0, 0, fmt.Errorf("config: no hay superficies en %s", filepath.Join(l.Dir, defaultSurfacesSubdir))
	}
	as, err := l.AppProfiles()
	if err != nil {
		return 0, 0, err
	}
	for _, s := range ss {
		if _, err := l.Load(s.Path); err != nil {
			return 0, 0, err
		}
		for _, a := range as {
			if _, err := l.Load(s.Path, a.Path); err != nil {
				return 0, 0, err
			}
		}
	}
	return len(ss), len(as), nil
}
