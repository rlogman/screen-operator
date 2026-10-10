package config

// Carga por capas: base.yaml <- superficie <- app <- overlay (SO_CONFIG_OVERLAY),
// validada contra schema.json al arrancar y en cada carga.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Variables de entorno que lee el paquete.
const (
	EnvOverlay             = "SO_CONFIG_OVERLAY"
	EnvDisableAppProfiles  = "SO_DISABLE_APP_PROFILES"
	defaultSchemaFile      = "schema.json"
	defaultBaseFile        = "base.yaml"
	defaultSurfacesSubdir  = "surfaces"
	defaultAppProfilesSubd = "apps"
)

// Config es la configuracion resuelta y validada de una tarea.
type Config struct {
	tree   map[string]any
	Layers []string // archivos aplicados, en orden
}

// Lookup devuelve el valor en una ruta con puntos, p. ej. "surface.input.click_hold_ms".
func (c *Config) Lookup(path string) (any, bool) {
	v, ok := lookup(c.tree, path)
	if !ok {
		return nil, false
	}
	return clone(v), true
}

// Tree devuelve una copia del arbol completo.
func (c *Config) Tree() map[string]any { return clone(c.tree).(map[string]any) }

// Decode copia el subarbol en path (todo el arbol si path es "") a out via JSON.
func (c *Config) Decode(path string, out any) error {
	v, ok := c.Lookup(path)
	if !ok {
		return fmt.Errorf("config: no existe la ruta %q", path)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Loader resuelve configuraciones a partir de un directorio con base.yaml,
// schema.json, surfaces/*.yaml y apps/*.yaml.
type Loader struct {
	Dir    string
	Getenv func(string) string
	schema *Schema
}

// NewLoader compila el esquema de dir. getenv nil usa os.Getenv.
func NewLoader(dir string, getenv func(string) string) (*Loader, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	raw, err := os.ReadFile(filepath.Join(dir, defaultSchemaFile))
	if err != nil {
		return nil, fmt.Errorf("config: no se pudo leer el esquema: %w", err)
	}
	s, err := CompileSchema(raw)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &Loader{Dir: dir, Getenv: getenv, schema: s}, nil
}

// BasePath es la ruta de base.yaml.
func (l *Loader) BasePath() string { return filepath.Join(l.Dir, defaultBaseFile) }

// LoadFiles fusiona exactamente los archivos dados, en orden, y valida el resultado.
// No agrega base ni overlay.
func (l *Loader) LoadFiles(paths ...string) (*Config, error) {
	trees := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		t, err := ReadLayer(p)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		trees = append(trees, t)
	}
	return l.build(paths, trees)
}

// Load fusiona base.yaml, las capas dadas (superficie, app) y el overlay de
// SO_CONFIG_OVERLAY si esta definido, y valida el resultado.
func (l *Loader) Load(layers ...string) (*Config, error) {
	paths := append([]string{l.BasePath()}, layers...)
	if ov, err := l.overlayPath(); err != nil {
		return nil, err
	} else if ov != "" {
		paths = append(paths, ov)
	}
	return l.LoadFiles(paths...)
}

// overlayPath devuelve la ruta del overlay o "" si no hay. Un overlay definido
// pero inexistente es error que nombra la ruta.
func (l *Loader) overlayPath() (string, error) {
	p := strings.TrimSpace(l.Getenv(EnvOverlay))
	if p == "" {
		return "", nil
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("config: %s=%q: el archivo no existe o no se puede leer: %w", EnvOverlay, p, err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("config: %s=%q: es un directorio, se esperaba un YAML", EnvOverlay, p)
	}
	return p, nil
}

func (l *Loader) build(paths []string, trees []map[string]any) (*Config, error) {
	merged := map[string]any{}
	for _, t := range trees {
		merged = Merge(merged, t)
	}
	if errs := l.schema.Validate(merged); len(errs) > 0 {
		return nil, &LoadError{Layers: paths, Errors: annotate(errs, paths, trees)}
	}
	return &Config{tree: merged, Layers: paths}, nil
}

// LoadError reune las violaciones del esquema de una carga.
type LoadError struct {
	Layers []string
	Errors []ValidationError
}

func (e *LoadError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: la configuracion no cumple config/schema.json (%d errores; capas: %s)",
		len(e.Errors), strings.Join(e.Layers, " <- "))
	for _, v := range e.Errors {
		b.WriteString("\n  ")
		b.WriteString(v.Error())
	}
	return b.String()
}

// Paths devuelve las rutas con error.
func (e *LoadError) Paths() []string {
	out := make([]string, len(e.Errors))
	for i, v := range e.Errors {
		out[i] = v.Path
	}
	return out
}

// annotate agrega al mensaje la ultima capa que define la ruta con error.
func annotate(errs []ValidationError, paths []string, trees []map[string]any) []ValidationError {
	out := make([]ValidationError, len(errs))
	for i, e := range errs {
		out[i] = e
		p := e.Path
		if j := strings.IndexByte(p, '['); j >= 0 {
			p = p[:j]
		}
		for k := len(trees) - 1; k >= 0; k-- {
			if _, ok := lookup(trees[k], p); ok && p != "" {
				out[i].Msg = fmt.Sprintf("%s [definida en %s]", e.Msg, paths[k])
				break
			}
		}
	}
	return out
}

// IsLoadError indica si err contiene violaciones del esquema.
func IsLoadError(err error) (*LoadError, bool) {
	var le *LoadError
	ok := errors.As(err, &le)
	return le, ok
}
