package screenoperator_test

// Tests de la historia 1.1: scaffold del modulo y gates locales (Makefile, .gitignore).
// Los gates se prueban sobre un modulo temporal con el mismo Makefile para no
// recursar sobre la suite del repositorio.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err != nil {
		t.Fatalf("el test debe correr en la raiz del modulo: %v", err)
	}
	return wd
}

// runMake ejecuta make en dir y devuelve salida combinada y codigo de salida.
func runMake(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("make %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// tempModule crea un modulo Go minimo con el Makefile del repositorio y los archivos dados.
func tempModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	mk, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"Makefile": string(mk),
		"go.mod":   "module tmpgate\n\ngo 1.22\n",
	}
	for k, v := range files {
		all[k] = v
	}
	for name, body := range all {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const okLib = "package tmpgate\n\n// Suma devuelve a+b.\nfunc Suma(a, b int) int { return a + b }\n"
const okTest = "package tmpgate\n\nimport \"testing\"\n\nfunc TestSuma(t *testing.T) {\n\tif Suma(1, 2) != 3 {\n\t\tt.Fatal(\"suma\")\n\t}\n}\n"

func TestE1_1_S1_MakeBuildProducesBinaries(t *testing.T) {
	root := repoRoot(t)
	bins := []string{"agent", "harness", "rfbprobe"}
	for _, b := range bins {
		_ = os.Remove(filepath.Join(root, "bin", b))
	}
	out, code := runMake(t, root, "build")
	if code != 0 {
		t.Fatalf("make build salio con %d:\n%s", code, out)
	}
	for _, b := range bins {
		fi, err := os.Stat(filepath.Join(root, "bin", b))
		if err != nil {
			t.Fatalf("falta bin/%s tras make build: %v\n%s", b, err, out)
		}
		if fi.Mode()&0o111 == 0 {
			t.Fatalf("bin/%s no es ejecutable", b)
		}
	}
}

func TestE1_1_S2_MakeGateRunsFmtVetTest(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		wantZero bool
		wantOut  []string
	}{
		{
			name:     "todo pasa",
			files:    map[string]string{"lib.go": okLib, "lib_test.go": okTest},
			wantZero: true,
			wantOut:  []string{"gofmt -l: ok", "go vet ./...", "go test ./...", "ok  \ttmpgate"},
		},
		{
			// Los worktrees de los agentes viven en .claude/worktrees: fmt-check no los recorre.
			name:     "ignora .claude",
			files:    map[string]string{"lib.go": okLib, "lib_test.go": okTest, ".claude/worktrees/x/feo.go": "package x\nfunc  Feo( ) int {return 1}\n"},
			wantZero: true,
			wantOut:  []string{"gofmt -l: ok"},
		},
		{
			name:    "gofmt lista un archivo",
			files:   map[string]string{"lib.go": okLib, "lib_test.go": okTest, "feo.go": "package tmpgate\nfunc  Feo( ) int {return 1}\n"},
			wantOut: []string{"feo.go"},
		},
		{
			name:    "go vet falla",
			files:   map[string]string{"lib.go": okLib, "lib_test.go": okTest, "vet.go": "package tmpgate\n\nimport \"fmt\"\n\n// Mal usa un verbo incorrecto.\nfunc Mal() string { return fmt.Sprintf(\"%d\", \"x\") }\n"},
			wantOut: []string{"go vet ./..."},
		},
		{
			name:    "go test falla",
			files:   map[string]string{"lib.go": okLib, "lib_test.go": okTest, "falla_test.go": "package tmpgate\n\nimport \"testing\"\n\nfunc TestFalla(t *testing.T) { t.Fatal(\"falla adrede\") }\n"},
			wantOut: []string{"go test ./...", "falla adrede"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempModule(t, tc.files)
			out, code := runMake(t, dir, "gate")
			if tc.wantZero && code != 0 {
				t.Fatalf("make gate salio con %d, se esperaba 0:\n%s", code, out)
			}
			if !tc.wantZero && code == 0 {
				t.Fatalf("make gate salio con 0, se esperaba fallo:\n%s", out)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(out, w) {
					t.Fatalf("la salida no contiene %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestE1_1_S3_MakeTestEpicRunsExactCommand(t *testing.T) {
	root := repoRoot(t)
	out, code := runMake(t, root, "-n", "test-epic", "E=3")
	if code != 0 {
		t.Fatalf("make -n test-epic salio con %d:\n%s", code, out)
	}
	var goLines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "go test") {
			goLines = append(goLines, strings.TrimSpace(l))
		}
	}
	want := "go test ./... -run '^TestE3_' -count=1"
	if len(goLines) != 1 || goLines[0] != want {
		t.Fatalf("comandos go test = %q, se esperaba exactamente [%q]", goLines, want)
	}

	// El codigo de salida sigue al de go test: falla si falla un TestE3_, ignora otras epicas.
	failing := "package tmpgate\n\nimport \"testing\"\n\nfunc TestE3_1_S1_Falla(t *testing.T) { t.Fatal(\"e3 falla\") }\n"
	otherEpic := "package tmpgate\n\nimport \"testing\"\n\nfunc TestE3_1_S1_Pasa(t *testing.T) {}\n\nfunc TestE4_1_S1_Falla(t *testing.T) { t.Fatal(\"no debe correr\") }\n"

	dir := tempModule(t, map[string]string{"lib.go": okLib, "e3_test.go": failing})
	out, code = runMake(t, dir, "test-epic", "E=3")
	if code == 0 || !strings.Contains(out, "e3 falla") {
		t.Fatalf("con un TestE3_ fallando se esperaba codigo != 0 (fue %d):\n%s", code, out)
	}
	dir = tempModule(t, map[string]string{"lib.go": okLib, "e3_test.go": otherEpic})
	out, code = runMake(t, dir, "test-epic", "E=3")
	if code != 0 || strings.Contains(out, "no debe correr") {
		t.Fatalf("solo deben correr los TestE3_ y pasar (codigo %d):\n%s", code, out)
	}
	// -count=1: la segunda corrida no sale de cache.
	out, _ = runMake(t, dir, "test-epic", "E=3")
	if strings.Contains(out, "(cached)") {
		t.Fatalf("test-epic uso resultados en cache:\n%s", out)
	}
}

func TestE1_1_S4_GitignoreCoversArtifacts(t *testing.T) {
	root := repoRoot(t)
	check := func(path string) bool {
		cmd := exec.Command("git", "check-ignore", "--no-index", "-q", path)
		cmd.Dir = root
		err := cmd.Run()
		if err == nil {
			return true
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return false
		}
		t.Fatalf("git check-ignore %s: %v", path, err)
		return false
	}
	for _, p := range []string{"bin/agent", "bin/", ".env", ".artifex/tickets.db", "traces/t1.jsonl", "traces/", "runs/r1.json", "runs/"} {
		if !check(p) {
			t.Errorf("%s deberia estar ignorado", p)
		}
	}
	for _, p := range []string{"config/base.yaml", "config/schema.json", "config/surfaces/vnc-kiosk.yaml", "docs/config.md", "docs/requirements.json"} {
		if check(p) {
			t.Errorf("%s no deberia estar ignorado", p)
		}
	}
}
