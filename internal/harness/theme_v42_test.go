package harness

// E10.3-S3 (incidente FR-0153): el corpus se reviso contra SIGECOM v4.2. Las
// instrucciones y claves de razon no pueden depender de etiquetas de la
// interfaz (que cambiaron de v3.9 a v4.2: Guardar -> Aplicar, Volver ->
// Cerrar, Buscar -> Filtrar, botones de crear, modal de novedades); los
// datos esperados no dependen del tema. Las evidencias cases/evidence/*.png
// son capturas de v3.9: quedan marcadas como tales en
// docs/incidente-FR-0153.md y sus datos se verificaron sobre v4.2 por muestreo.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// uiLabels son etiquetas de botones o dialogos de cualquiera de los dos temas.
// Una instruccion o clave que las nombre dependeria del tema.
var uiLabels = regexp.MustCompile(`(?i)\b(guardar|volver|buscar|aplicar|filtrar|limpiar|entendido|nuevo cliente|nuevo pedido|crear cliente|crear pedido|novedades|v3\.9|v4\.2)\b`)

func TestE10_3_S3_CorpusDoesNotDependOnTheme(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("sin casos en %s: %v", corpusDir, err)
	}
	official, _ := filepath.Glob("../../material/pruebas/*.json")
	for _, f := range append(files, official...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			ID          string   `json:"id"`
			Instruccion string   `json:"instruccion"`
			Claves      []string `json:"claves_razon"`
			Esperado    string   `json:"estado_esperado"`
			Diff        []struct {
				Campo string `json:"campo"`
			} `json:"diff_esperado"`
		}
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		texts := append([]string{c.Instruccion}, c.Claves...)
		for _, s := range texts {
			if m := uiLabels.FindString(s); m != "" {
				t.Errorf("%s: %q nombra la etiqueta de interfaz %q (depende del tema); actualizarlo o marcarlo", filepath.Base(f), s, m)
			}
		}
		// Los datos esperados son de la base (campos de /__check), no de la UI.
		for _, d := range c.Diff {
			if uiLabels.MatchString(d.Campo) || strings.Contains(d.Campo, " ") {
				t.Errorf("%s: campo esperado %q no es un campo de datos", filepath.Base(f), d.Campo)
			}
		}
	}
}
