package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const repoConfig = "../../config"

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func newTestLoader(t *testing.T, kv map[string]string) *Loader {
	t.Helper()
	l, err := NewLoader(repoConfig, env(kv))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func writeLayer(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "capa.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	surfacePath = filepath.Join(repoConfig, "surfaces", "vnc-kiosk.yaml")
	appPath     = filepath.Join(repoConfig, "apps", "sigecom.yaml")
)

func mustLookup(t *testing.T, c *Config, path string) any {
	t.Helper()
	v, ok := c.Lookup(path)
	if !ok {
		t.Fatalf("falta %s", path)
	}
	return v
}

func TestE1_2_S1_LayersLoadInOrder(t *testing.T) {
	l := newTestLoader(t, nil)
	c, err := l.LoadFiles(l.BasePath(), surfacePath, appPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLookup(t, c, "surface.input.click_hold_ms"); got != int64(150) {
		t.Errorf("surface.input.click_hold_ms = %v (%T), se esperaba 150", got, got)
	}
	if got := mustLookup(t, c, "budgets.task_deadline_s"); got != int64(300) {
		t.Errorf("budgets.task_deadline_s = %v, se esperaba 300", got)
	}
	if got := mustLookup(t, c, "app.id"); got != "sigecom" {
		t.Errorf("app.id = %v, se esperaba sigecom", got)
	}
	// Load agrega base y (sin overlay) produce lo mismo.
	c2, err := l.Load(surfacePath, appPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Tree(), c2.Tree()) {
		t.Error("Load(superficie, app) difiere de LoadFiles(base, superficie, app)")
	}
	var hold struct {
		ClickHoldMS int `json:"click_hold_ms"`
	}
	if err := c.Decode("surface.input", &hold); err != nil || hold.ClickHoldMS != 150 {
		t.Errorf("Decode surface.input = %+v, %v", hold, err)
	}
}

func TestE1_2_S2_DeepMergeMapsReplaceLists(t *testing.T) {
	base := map[string]any{"a": map[string]any{
		"b": int64(1),
		"c": map[string]any{"d": int64(1), "e": int64(2)},
		"l": []any{int64(1), int64(2), int64(3)},
	}}
	layer := map[string]any{"a": map[string]any{
		"c": map[string]any{"e": int64(9), "f": int64(3)},
		"l": []any{int64(7)},
	}}
	got := Merge(base, layer)
	want := map[string]any{"a": map[string]any{
		"b": int64(1),
		"c": map[string]any{"d": int64(1), "e": int64(9), "f": int64(3)},
		"l": []any{int64(7)},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %#v\nse esperaba %#v", got, want)
	}
	if !reflect.DeepEqual(base["a"].(map[string]any)["l"], []any{int64(1), int64(2), int64(3)}) {
		t.Fatal("Merge modifico la base")
	}

	// Sobre la configuracion real: la lista de la capa reemplaza completa y los
	// mapas hermanos se conservan.
	l := newTestLoader(t, nil)
	p := writeLayer(t, "policy:\n  destructive:\n    keywords: [purgar]\n")
	c, err := l.Load(surfacePath, p)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLookup(t, c, "policy.destructive.keywords"); !reflect.DeepEqual(got, []any{"purgar"}) {
		t.Errorf("keywords = %v, se esperaba [purgar]", got)
	}
	if got := mustLookup(t, c, "policy.destructive.bulk_changes"); got != "fail" {
		t.Errorf("bulk_changes = %v, se esperaba fail (conservado de la base)", got)
	}
	if got := mustLookup(t, c, "policy.destructive.require_unique_target"); got != true {
		t.Errorf("require_unique_target = %v, se esperaba true", got)
	}
}

func TestE1_2_S3_UnknownKeyFails(t *testing.T) {
	l := newTestLoader(t, nil)
	p := writeLayer(t, "surface:\n  input:\n    clik_hold_ms: 150\n")
	_, err := l.Load(surfacePath, p)
	if err == nil {
		t.Fatal("se esperaba error por clave desconocida")
	}
	if !strings.Contains(err.Error(), "surface.input.clik_hold_ms") {
		t.Fatalf("el error no nombra surface.input.clik_hold_ms: %v", err)
	}
	le, ok := IsLoadError(err)
	if !ok || !contains(le.Paths(), "surface.input.clik_hold_ms") {
		t.Fatalf("se esperaba LoadError con la ruta; fue %#v", err)
	}
}

func TestE1_2_S4_OutOfRangeTemperatureFails(t *testing.T) {
	l := newTestLoader(t, nil)
	p := writeLayer(t, "models:\n  roles:\n    actor:\n      temperature: 3\n")
	_, err := l.Load(surfacePath, p)
	le, ok := IsLoadError(err)
	if !ok {
		t.Fatalf("se esperaba error de validacion, fue %v", err)
	}
	if !contains(le.Paths(), "models.roles.actor.temperature") || !strings.Contains(err.Error(), "models.roles.actor.temperature") {
		t.Fatalf("el error no nombra models.roles.actor.temperature: %v", err)
	}
	// El limite del esquema (maximum 2) es inclusivo.
	ok2 := writeLayer(t, "models:\n  roles:\n    actor:\n      temperature: 2\n")
	if _, err := l.Load(surfacePath, ok2); err != nil {
		t.Fatalf("temperature=2 deberia ser valida: %v", err)
	}
}

func TestE1_2_S5_OverlayEnvOverridesActorOnly(t *testing.T) {
	ov := writeLayer(t, "models:\n  roles:\n    actor:\n      model: x/y\n")
	plain, err := newTestLoader(t, nil).Load(surfacePath, appPath)
	if err != nil {
		t.Fatal(err)
	}
	withOv, err := newTestLoader(t, map[string]string{EnvOverlay: ov}).Load(surfacePath, appPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLookup(t, withOv, "models.roles.actor.model"); got != "x/y" {
		t.Fatalf("actor = %v, se esperaba x/y", got)
	}
	a, b := plain.Tree(), withOv.Tree()
	delete(a["models"].(map[string]any)["roles"].(map[string]any)["actor"].(map[string]any), "model")
	delete(b["models"].(map[string]any)["roles"].(map[string]any)["actor"].(map[string]any), "model")
	if !reflect.DeepEqual(a, b) {
		t.Fatal("el overlay cambio valores distintos de models.roles.actor.model")
	}
	if withOv.Layers[len(withOv.Layers)-1] != ov {
		t.Fatalf("el overlay debe aplicarse al final; capas: %v", withOv.Layers)
	}
}

func TestE1_2_S6_MissingOverlayFailsLoad(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-existe.yaml")
	_, err := newTestLoader(t, map[string]string{EnvOverlay: missing}).Load(surfacePath)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("se esperaba error que nombre %s, fue %v", missing, err)
	}
	// El proceso cmd/agent se prueba en cmd/agent (TestE1_2_S6_AgentExitsOnMissingOverlay).
}

func TestSchemaRejectsUnknownKeyword(t *testing.T) {
	_, err := CompileSchema([]byte(`{"type":"object","patternProperties":{}}`))
	if err == nil || !strings.Contains(err.Error(), "patternProperties") {
		t.Fatalf("se esperaba error por palabra clave no soportada, fue %v", err)
	}
}

func TestSchemaCatchesTypeEnumRequired(t *testing.T) {
	l := newTestLoader(t, nil)
	p := writeLayer(t, "surface:\n  transport: rdp\n  screen:\n    width: \"1280\"\nbudgets:\n  max_cost_usd: 0\n")
	_, err := l.Load(surfacePath, p)
	le, ok := IsLoadError(err)
	if !ok {
		t.Fatalf("se esperaba LoadError, fue %v", err)
	}
	for _, want := range []string{"surface.transport", "surface.screen.width", "budgets.max_cost_usd"} {
		if !contains(le.Paths(), want) {
			t.Errorf("falta error en %s: %v", want, err)
		}
	}
	// Una capa sin base deja claves requeridas sin definir.
	_, err = l.LoadFiles(surfacePath)
	if le, ok := IsLoadError(err); !ok || !contains(le.Paths(), "contract") {
		t.Fatalf("se esperaba error por clave requerida contract, fue %v", err)
	}
}

func TestParseLayerRejectsDuplicateKeys(t *testing.T) {
	if _, err := ParseLayer([]byte("a: 1\na: 2\n")); err == nil || !strings.Contains(err.Error(), "duplicada") {
		t.Fatalf("se esperaba error por clave duplicada, fue %v", err)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
