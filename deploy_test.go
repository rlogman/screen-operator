package screenoperator_test

// Tests de la historia 4.1 (imagen de contenedor) que no necesitan el daemon
// de Docker: forma del Dockerfile, .dockerignore y build reproducible del
// binario estatico que la etapa final copia. Los tests contra la imagen real
// viven en deploy_docker_test.go (build tag docker).

import (
	"bufio"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dfInstr es una instruccion del Dockerfile con su etapa (0 = primera).
type dfInstr struct {
	stage int
	cmd   string // en mayusculas: FROM, RUN, COPY, ENV...
	args  string
}

// parseDockerfile lee el Dockerfile uniendo continuaciones con '\' y
// descartando comentarios. Suficiente para el Dockerfile del repo.
func parseDockerfile(t *testing.T, path string) []dfInstr {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []dfInstr
	stage := -1
	var buf strings.Builder
	flush := func() {
		line := strings.TrimSpace(buf.String())
		buf.Reset()
		if line == "" {
			return
		}
		cmd, args, _ := strings.Cut(line, " ")
		cmd = strings.ToUpper(cmd)
		if cmd == "FROM" {
			stage++
		}
		out = append(out, dfInstr{stage: stage, cmd: cmd, args: strings.TrimSpace(args)})
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if strings.HasSuffix(l, `\`) {
			buf.WriteString(strings.TrimSuffix(l, `\`) + " ")
			continue
		}
		buf.WriteString(l)
		flush()
	}
	flush()
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func goModVersion(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("go.mod sin directiva go")
	}
	return string(m[1])
}

func TestE4_1_S1_DockerfileMultiStageSinToolchain(t *testing.T) {
	root := repoRoot(t)
	ins := parseDockerfile(t, filepath.Join(root, "Dockerfile"))
	var froms []dfInstr
	for _, in := range ins {
		if in.cmd == "FROM" {
			froms = append(froms, in)
		}
	}
	if len(froms) < 2 {
		t.Fatalf("se esperaba un Dockerfile multi-stage, hay %d FROM", len(froms))
	}
	// La etapa de build usa la misma version de Go que go.mod.
	want := "golang:" + goModVersion(t, root)
	if !strings.HasPrefix(froms[0].args, want) {
		t.Fatalf("la etapa de build es %q, se esperaba %s*", froms[0].args, want)
	}
	// La etapa final no es una imagen de Go ni copia el toolchain.
	last := len(froms) - 1
	final := strings.Fields(froms[last].args)[0]
	if strings.Contains(final, "golang") {
		t.Fatalf("la etapa final es una imagen de Go: %s", final)
	}
	if !strings.HasPrefix(final, "gcr.io/distroless/static") {
		t.Fatalf("la etapa final deberia ser distroless static (sin shell ni toolchain): %s", final)
	}
	var cgoOff, entry, user bool
	for _, in := range ins {
		if in.stage == 0 && in.cmd == "ENV" && strings.Contains(in.args, "CGO_ENABLED=0") {
			cgoOff = true
		}
		if in.stage != last {
			continue
		}
		switch in.cmd {
		case "COPY":
			if strings.Contains(in.args, "/usr/local/go") || strings.Contains(in.args, "/go/pkg") {
				t.Fatalf("la etapa final copia el toolchain: COPY %s", in.args)
			}
		case "RUN":
			t.Fatalf("la etapa final no debe tener RUN (distroless sin shell): RUN %s", in.args)
		case "ENTRYPOINT":
			entry = in.args == `["/app/agent"]`
		case "USER":
			user = strings.HasPrefix(in.args, "nonroot")
		case "ENV", "ARG":
			if strings.Contains(in.args, "TEAM_TOKEN") || strings.Contains(in.args, "OPENROUTER_API_KEY") {
				t.Fatalf("la imagen no debe declarar secretos: %s %s", in.cmd, in.args)
			}
		}
	}
	if !cgoOff {
		t.Fatal("la etapa de build debe fijar CGO_ENABLED=0 (binario estatico)")
	}
	if !entry {
		t.Fatal(`la etapa final debe tener ENTRYPOINT ["/app/agent"]`)
	}
	if !user {
		t.Fatal("la etapa final debe correr como nonroot")
	}
}

func TestE4_1_S1_DockerignoreExcluyeSecretos(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "!") {
			t.Fatalf(".dockerignore no debe re-incluir nada: %q", l)
		}
		have[strings.TrimSuffix(l, "/")] = true
	}
	for _, p := range []string{".env", ".git", ".claude", ".artifex", "material/*.zip", "bin", "traces", "runs"} {
		if !have[p] {
			t.Errorf(".dockerignore no excluye %q", p)
		}
	}
}

// dockerLdflags extrae -ldflags del RUN go build del Dockerfile para compilar
// igual que la imagen.
func dockerLdflags(t *testing.T, root string) string {
	t.Helper()
	for _, in := range parseDockerfile(t, filepath.Join(root, "Dockerfile")) {
		if in.cmd == "RUN" && strings.Contains(in.args, "go build") {
			if !strings.Contains(in.args, "-trimpath") {
				t.Fatal("el go build del Dockerfile debe usar -trimpath (build reproducible)")
			}
			m := regexp.MustCompile(`-ldflags="([^"]*)"`).FindStringSubmatch(in.args)
			if m == nil {
				t.Fatal("el go build del Dockerfile no fija -ldflags")
			}
			return m[1]
		}
	}
	t.Fatal("el Dockerfile no tiene RUN go build")
	return ""
}

func sha256File(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Readiness sin daemon: el binario que copia la etapa final se compila igual
// que en la imagen (linux, CGO_ENABLED=0, mismos flags), es estatico y el
// build es reproducible (dos builds, mismo sha256).
func TestE4_1_S1_BinarioEstaticoReproducible(t *testing.T) {
	root := repoRoot(t)
	ldflags := dockerLdflags(t, root)
	build := func(out string) {
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", out, "./cmd/agent")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build linux/amd64: %v\n%s", err, b)
		}
	}
	a := filepath.Join(t.TempDir(), "agent")
	b := filepath.Join(t.TempDir(), "agent")
	build(a)
	build(b)
	if ha, hb := sha256File(t, a), sha256File(t, b); ha != hb {
		t.Fatalf("build no reproducible: %s != %s", ha, hb)
	}
	f, err := elf.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			t.Fatal("el binario tiene PT_INTERP: no es estatico")
		}
	}
	libs, err := f.ImportedLibraries()
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 0 {
		t.Fatalf("el binario enlaza bibliotecas dinamicas: %v", libs)
	}
}
