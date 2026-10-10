// Command harness corre casos de evaluacion contra el agente usando /__reset y
// /__check del escritorio, puntua localmente y reporta metricas (epica 5).
//
//	harness run [--cases 'material/pruebas/*.json'] [--runs 1] [--agent http://127.0.0.1:8080] [--out runs]
//
// Secretos solo por entorno, nunca por argumentos: TEAM_TOKEN (Bearer del
// agente), SO_DESKTOP_URL, SO_DESKTOP_PASSWORD, SO_DESKTOP_TOKEN y, si existe,
// OPENROUTER_API_KEY (solo para detectar fugas en los resumenes).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"screenoperator/internal/harness"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, time.Now, os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "uso: harness run [flags]   (harness run -h para ver los flags)")
}

func run(ctx context.Context, args []string, getenv func(string) string, now func() time.Time, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return cmdRun(ctx, args[1:], getenv, now, stdout, stderr)
	default:
		usage(stderr)
		return 2
	}
}

// newRunDir crea runs/<fecha-hora>/ (con sufijo si ya existe).
func newRunDir(root string, now time.Time) (string, error) {
	base := filepath.Join(root, now.Format("2006-01-02_150405"))
	dir := base
	for i := 2; ; i++ {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if os.IsNotExist(err) {
			if err := os.MkdirAll(root, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if !os.IsExist(err) {
			return "", err
		}
		dir = fmt.Sprintf("%s-%d", base, i)
	}
}

func cmdRun(ctx context.Context, args []string, getenv func(string) string, now func() time.Time, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("harness run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cases := fs.String("cases", "material/pruebas/*.json", "patrones glob separados por comas")
	runs := fs.Int("runs", 1, "corridas por caso (reset previo en cada una)")
	agentURL := fs.String("agent", "http://127.0.0.1:8080", "URL base del agente")
	outRoot := fs.String("out", "runs", "directorio raiz de resultados")
	poll := fs.Duration("poll", 5*time.Second, "intervalo de consulta de GET /tasks/{id}")
	timeout := fs.Duration("timeout", 300*time.Second, "limite por tarea")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	desk, pass, dtok, atok := getenv("SO_DESKTOP_URL"), getenv("SO_DESKTOP_PASSWORD"), getenv("SO_DESKTOP_TOKEN"), getenv("TEAM_TOKEN")
	for name, v := range map[string]string{"SO_DESKTOP_URL": desk, "SO_DESKTOP_TOKEN": dtok, "TEAM_TOKEN": atok} {
		if v == "" {
			fmt.Fprintf(stderr, "harness: falta la variable de entorno %s\n", name)
			return 2
		}
	}
	base, ws, err := harness.DesktopBase(desk)
	if err != nil {
		fmt.Fprintln(stderr, "harness: SO_DESKTOP_URL:", err)
		return 2
	}
	cs, warns, err := harness.LoadCases(*cases)
	for _, w := range warns {
		fmt.Fprintln(stderr, "harness: aviso:", w)
	}
	if err != nil {
		fmt.Fprintln(stderr, "harness:", err)
		return 2
	}
	dir, err := newRunDir(*outRoot, now())
	if err != nil {
		fmt.Fprintln(stderr, "harness:", err)
		return 1
	}
	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		fmt.Fprintln(stderr, "harness:", err)
		return 1
	}
	defer f.Close()
	r := &harness.Runner{
		Desktop:      &harness.Desktop{BaseURL: base, Token: dtok},
		Agent:        &harness.Agent{BaseURL: *agentURL, Token: atok},
		DesktopWS:    ws,
		Password:     pass,
		Secrets:      []string{atok, dtok, getenv("OPENROUTER_API_KEY")},
		Runs:         *runs,
		PollInterval: *poll,
		Timeout:      *timeout,
		Out:          f,
		Log:          func(format string, a ...any) { fmt.Fprintf(stdout, "harness: "+format+"\n", a...) },
	}
	recs, err := r.Run(ctx, cs)
	if err != nil {
		fmt.Fprintln(stderr, "harness:", err)
		return 1
	}
	fmt.Fprintf(stdout, "harness: %d resultados en %s\n", len(recs), filepath.Join(dir, "results.jsonl"))
	return 0
}
