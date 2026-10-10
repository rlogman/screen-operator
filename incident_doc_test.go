package screenoperator_test

// Tests de la epica 10 (incidente FR-0153) sobre el documento del incidente y
// la medicion sobre v4.2.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func incidentDoc(t *testing.T) (string, string) {
	t.Helper()
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "docs", "incidente-FR-0153.md"))
	if err != nil {
		t.Fatalf("falta docs/incidente-FR-0153.md: %v", err)
	}
	return root, string(b)
}

// E10.3-S2: la medicion sobre v4.2 (oficiales + corpus, perfiles apagados)
// tiene su runs/<fecha>/report.md y el documento lo enlaza. runs/ no se
// versiona: la copia versionada vive en docs/incidente/medicion-v4.2/ y el
// documento enlaza ambas.
func TestE10_3_S2_DocLinksV42HarnessReport(t *testing.T) {
	root, doc := incidentDoc(t)
	runLink := regexp.MustCompile(`runs/\d{4}-\d{2}-\d{2}_\d{6}(-\d+)?/report\.md`).FindString(doc)
	if runLink == "" {
		t.Fatal("el documento no enlaza runs/<fecha>/report.md")
	}
	if !strings.Contains(doc, "SO_DISABLE_APP_PROFILES=1") {
		t.Fatal("el documento no dice que la medicion fue con SO_DISABLE_APP_PROFILES=1")
	}
	copyPath := filepath.Join(root, "docs", "incidente", "medicion-v4.2", "report.md")
	b, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("falta la copia versionada del reporte: %v", err)
	}
	rep := string(b)
	if !strings.Contains(doc, "incidente/medicion-v4.2/report.md") {
		t.Fatal("el documento no enlaza la copia versionada del reporte")
	}
	// Oficiales (grupo "oficial") y casos del corpus (grupos por categoria).
	if !strings.Contains(rep, "| oficial |") {
		t.Error("el reporte no incluye los casos oficiales (material/pruebas/)")
	}
	corpus := false
	for _, g := range []string{"| crear |", "| editar |", "| consultar |", "| exportar |", "| rechazar |", "| robustez |"} {
		corpus = corpus || strings.Contains(rep, g)
	}
	if !corpus {
		t.Error("el reporte no incluye casos del corpus (cases/)")
	}
	// Si la corrida esta en este checkout, la copia es la misma.
	if orig, err := os.ReadFile(filepath.Join(root, runLink)); err == nil && string(orig) != rep {
		t.Errorf("%s y la copia versionada difieren", runLink)
	}
}

// E10.4-S1: el documento explica que cambio (capturas antes y despues), como
// lo detecto el agente, que fue configuracion y que codigo, y la medicion
// antes y despues.
func TestE10_4_S1_IncidentDocExplainsChange(t *testing.T) {
	root, doc := incidentDoc(t)
	for _, want := range []string{
		"## 1. Qué cambió", "## 2. Cómo lo detecta el agente", "## 3. Qué fue configuración y qué fue código",
		"## 4. Medición antes y después",
		"cambio de app detectado: v3.9 -> v4.2", "diálogo informativo cerrado",
		"config/apps/sigecom.yaml", "internal/agent/opening.go",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("el documento no contiene %q", want)
		}
	}
	if strings.Contains(doc, "MEDICION_PENDIENTE") {
		t.Error("la medicion sigue pendiente en el documento")
	}
	imgs := regexp.MustCompile(`\]\((incidente/[^)]+\.png)\)`).FindAllStringSubmatch(doc, -1)
	var before, after bool
	for _, m := range imgs {
		p := filepath.Join(root, "docs", m[1])
		if _, err := os.Stat(p); err != nil {
			t.Errorf("captura enlazada inexistente: %s", m[1])
		}
		before = before || strings.Contains(m[1], "v3.9")
		after = after || strings.Contains(m[1], "v4.2")
	}
	if !before || !after {
		t.Errorf("faltan capturas antes (v3.9=%v) y despues (v4.2=%v)", before, after)
	}
	// Antes: corrida 2 de la epica 6 (2/5 con un falso exito); despues: esta medicion.
	if !strings.Contains(doc, "2/5") || !strings.Contains(doc, "Después") {
		t.Error("el documento no muestra la medicion antes (2/5) y despues")
	}
}
