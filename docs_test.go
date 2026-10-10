package screenoperator_test

// Tests de la epica 9: README publico y guion del video.

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// TestE9_1_S1_ReadmeHasRequiredSections: quien no participo encuentra como
// compilar, correr el agente con variables de entorno, correr el harness y el
// ultimo reporte de metricas.
func TestE9_1_S1_ReadmeHasRequiredSections(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	sections := []string{
		"## Arquitectura en breve",
		"## Compilar",
		"## Correr el agente en local",
		"## Contrato del entrypoint",
		"## Configuración declarativa por capas",
		"## Harness y corpus",
		"## Guardrails",
		"## Respuesta al incidente FR-0153",
		"## Resultados medidos",
	}
	for _, s := range sections {
		if !strings.Contains(readme, "\n"+s+"\n") {
			t.Errorf("falta la seccion %q", s)
		}
	}
	for _, want := range []string{
		"make build",
		"TEAM_TOKEN", "OPENROUTER_API_KEY", "SO_CONFIG_DIR", "SO_TRACE_DIR", "SO_DISABLE_APP_PROFILES",
		"bin/harness run",
		"docs/incidente-FR-0153.md",
		"docs/design/diagrams/",
		"https://screen-operator-production.up.railway.app",
		"Se actualiza antes de la entrega",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("el README no menciona %q", want)
		}
	}
	// Los enlaces locales a archivos que existen en el repo deben resolver
	// (docs/incidente-FR-0153.md lo escribe la epica 10 y puede faltar).
	re := regexp.MustCompile(`\]\(([^)#\s]+)\)`)
	for _, m := range re.FindAllStringSubmatch(readme, -1) {
		link := m[1]
		if strings.Contains(link, "://") || link == "docs/incidente-FR-0153.md" {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot(t), link)); err != nil {
			t.Errorf("enlace roto en el README: %s", link)
		}
	}
	// La tabla de resultados lleva fecha y fuente.
	results := readme[strings.Index(readme, "## Resultados medidos"):]
	for _, want := range []string{"2026-10-10", "fuente", "pendiente de la medición final"} {
		if !strings.Contains(results, want) {
			t.Errorf("Resultados medidos no incluye %q", want)
		}
	}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-or-v1-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`ftr_[A-Za-z0-9]{20,}`),
}

// envValues devuelve los valores de .env (si existe) con longitud suficiente
// para que una coincidencia sea un secreto y no una palabra comun.
func envValues(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot(t), ".env"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if len(v) >= 8 {
			out = append(out, v)
		}
	}
	return out
}

// checkNoSecrets informa (sin imprimir el valor) si text contiene un secreto.
func checkNoSecrets(t *testing.T, name, text string, values []string) {
	t.Helper()
	for _, re := range secretPatterns {
		if re.MatchString(text) {
			t.Errorf("%s contiene un valor con el patron %s", name, re)
		}
	}
	for i, v := range values {
		if strings.Contains(text, v) {
			t.Errorf("%s contiene el valor #%d de .env", name, i+1)
		}
	}
}

// TestE9_1_S2_ReadmeHasNoSecretsOrPrivateURLs: sin contraseñas, tokens ni URLs
// privadas del escritorio.
func TestE9_1_S2_ReadmeHasNoSecretsOrPrivateURLs(t *testing.T) {
	values := envValues(t)
	for _, f := range []string{"README.md", "docs/video-guion.md"} {
		text := readRepoFile(t, f)
		checkNoSecrets(t, f, text, values)
		// Ningun escritorio concreto: solo marcadores <...> en los wss://.
		for _, m := range regexp.MustCompile(`wss?://[^\s"')]+`).FindAllString(text, -1) {
			if !strings.HasPrefix(m, "wss://<") {
				t.Errorf("%s: URL de escritorio que no es un marcador: wss://...", f)
			}
		}
		// Las variables sensibles solo aparecen sin valor en los ejemplos.
		for _, m := range regexp.MustCompile(`(?m)(TEAM_TOKEN|OPENROUTER_API_KEY|SO_DESKTOP_PASSWORD|SO_DESKTOP_TOKEN|SO_DESKTOP_URL)=(\S+)`).FindAllStringSubmatch(text, -1) {
			if m[2] != "..." {
				t.Errorf("%s: %s se asigna a un valor concreto; usa ...", f, m[1])
			}
		}
	}
}

// TestE9_1_S3_NoSecretsInTrackedFiles: un grep de patrones de secretos sobre
// los archivos versionados (y los valores de .env, si existe) no encuentra nada.
func TestE9_1_S3_NoSecretsInTrackedFiles(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files no disponible: %v", err)
	}
	values := envValues(t)
	scanned := 0
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || strings.HasSuffix(rel, ".png") || strings.HasSuffix(rel, ".pdf") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || len(b) > 2<<20 {
			continue
		}
		scanned++
		// Este archivo declara los patrones y no cuenta.
		if rel == "docs_test.go" {
			continue
		}
		checkNoSecrets(t, rel, string(b), values)
	}
	if scanned == 0 {
		t.Fatal("no se escaneo ningun archivo")
	}
}

// TestE9_2_S1_VideoScriptFitsInTwoMinutes: bloques con tiempos que suman como
// maximo 120 s y cubren los temas exigidos.
func TestE9_2_S1_VideoScriptFitsInTwoMinutes(t *testing.T) {
	text := readRepoFile(t, "docs/video-guion.md")
	re := regexp.MustCompile(`(?m)^Tiempo: (\d+):(\d{2})-(\d+):(\d{2}) \((\d+) s\)$`)
	blocks := re.FindAllStringSubmatch(text, -1)
	if len(blocks) < 5 {
		t.Fatalf("se esperaban al menos 5 bloques con tiempo, hay %d", len(blocks))
	}
	atoi := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	sum, prevEnd := 0, 0
	for i, b := range blocks {
		start, end, n := atoi(b[1])*60+atoi(b[2]), atoi(b[3])*60+atoi(b[4]), atoi(b[5])
		if end-start != n {
			t.Errorf("bloque %d: %d-%d no dura %d s", i+1, start, end, n)
		}
		if start != prevEnd {
			t.Errorf("bloque %d empieza en %d s y el anterior termino en %d s", i+1, start, prevEnd)
		}
		prevEnd = end
		sum += n
	}
	if sum > 120 || prevEnd > 120 {
		t.Errorf("el guion dura %d s (fin en %d s); maximo 120 s", sum, prevEnd)
	}
	lower := strings.ToLower(text)
	for _, topic := range []string{"problema", "arquitectura", "tarea en vivo", "homónimos", "incidente", "métricas"} {
		if !strings.Contains(lower, "## bloque") || !strings.Contains(lower, topic) {
			t.Errorf("el guion no cubre %q", topic)
		}
	}
	for _, step := range []string{"detecta", "adapta", "explicamos"} {
		if !strings.Contains(lower, step) {
			t.Errorf("el bloque del incidente no cubre %q (detectar, adaptar, explicar)", step)
		}
	}
	// Cada bloque trae pantalla y texto.
	if strings.Count(text, "\nPantalla: ") < len(blocks) || strings.Count(text, "\nTexto:") < len(blocks) {
		t.Error("cada bloque debe traer 'Pantalla:' y 'Texto:'")
	}
}
