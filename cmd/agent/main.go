// Command agent expone el contrato HTTP del evento y opera escritorios por VNC.
//
// Al arrancar valida toda la configuracion (SO_CONFIG_DIR, por defecto ./config,
// mas el overlay de SO_CONFIG_OVERLAY). Si falla, termina con codigo 1. Exige
// TEAM_TOKEN (contract.auth.token_env) y escucha en :PORT (8080 por defecto).
//
// Cada tarea la ejecuta el agente real (internal/agent): el DriverDialer abre la
// sesion RFB del escritorio que llega en POST /tasks y el Runner corre el loop
// por fases con el modelo de models.roles (OpenRouter, OPENROUTER_API_KEY).
// Sin OPENROUTER_API_KEY el agente arranca igual (el contrato responde) pero
// cada tarea termina "fallida" sin tocar el escritorio, y se avisa en el log.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"screenoperator/internal/agent"
	"screenoperator/internal/api"
	"screenoperator/internal/config"
	"screenoperator/internal/guard"
	"screenoperator/internal/llm"
	"screenoperator/internal/tasks"
	"screenoperator/internal/trace"
)

// EnvConfigDir es el directorio con base.yaml, schema.json, surfaces/ y apps/.
const EnvConfigDir = "SO_CONFIG_DIR"

// EnvPort es el puerto HTTP. DefaultPort si no esta definido.
const (
	EnvPort     = "PORT"
	DefaultPort = "8080"
)

// listenFunc abre el listener; los tests lo reemplazan (el sandbox no deja bindear).
type listenFunc func(network, addr string) (net.Listener, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, net.Listen))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, listen listenFunc) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	checkOnly := fs.Bool("check-config", false, "valida la configuracion y sale")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir := getenv(EnvConfigDir)
	if dir == "" {
		dir = "config"
	}
	loader, err := config.NewLoader(dir, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}
	ns, na, err := loader.CheckAll()
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}
	if *checkOnly {
		fmt.Fprintf(stdout, "agent: configuracion valida (%s: %d superficies, %d perfiles de app)\n", dir, ns, na)
		return 0
	}

	cfg, err := baseConfig(loader)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}
	contract, err := api.ContractFromConfig(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}
	token := strings.TrimSpace(getenv(contract.TokenEnv))
	if token == "" {
		fmt.Fprintf(stderr, "agent: falta la variable de entorno %s (token Bearer del equipo)\n", contract.TokenEnv)
		return 1
	}
	addr, err := listenAddr(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}

	red, err := redactor(cfg, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}
	log := tasks.NewLogger(stdout, red)
	budget, err := budgetFrom(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}
	mgr := tasks.NewManager(tasks.Options{
		Runner:   &agent.Runner{Loader: loader, Getenv: getenv, NewGuard: guard.New, GuardFor: guard.ForTask(getenv)},
		Dialer:   &agent.DriverDialer{Loader: loader},
		Budget:   budget,
		Logger:   log,
		Redactor: red,
		// Trazas por paso solo si SO_TRACE_DIR esta definida.
		TraceDir: strings.TrimSpace(getenv(trace.EnvDir)),
	})
	srv, err := api.NewServer(contract, token, mgr, log)
	if err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	}

	ln, err := listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(stderr, "agent: no se pudo escuchar en %s: %v\n", addr, err)
		return 1
	}
	if models, err := llm.ModelsFromConfig(cfg); err != nil {
		fmt.Fprintln(stderr, "agent:", err)
		return 1
	} else if strings.TrimSpace(getenv(models.APIKeyEnv)) == "" {
		log.Warn("falta la clave del modelo: las tareas terminaran fallidas sin tocar el escritorio",
			tasks.LogKeyPhase, "arranque", "variable", models.APIKeyEnv)
	}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Info("agente escuchando", tasks.LogKeyPhase, "arranque", "addr", addr,
		"superficies", ns, "perfiles_app", na, "deadline_ms", budget.Deadline().Milliseconds())

	errCh := make(chan error, 1)
	go func() { errCh <- hs.Serve(ln) }()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(stderr, "agent: servidor:", err)
			return 1
		}
		return 0
	case <-ctx.Done():
	}
	log.Info("apagando", tasks.LogKeyPhase, "apagado")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	_ = mgr.Shutdown(sctx)
	return 0
}

// baseConfig resuelve base <- primera superficie <- overlay. Contract, summary y
// budgets viven en base.yaml; la superficie solo completa lo que el esquema exige.
func baseConfig(l *config.Loader) (*config.Config, error) {
	ss, err := l.Surfaces()
	if err != nil {
		return nil, err
	}
	if len(ss) == 0 {
		return nil, errors.New("config: no hay superficies en surfaces/")
	}
	return l.Load(ss[0].Path)
}

// listenAddr devuelve ":PORT", con 8080 por defecto.
func listenAddr(getenv func(string) string) (string, error) {
	p := strings.TrimSpace(getenv(EnvPort))
	if p == "" {
		p = DefaultPort
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s=%q no es un puerto valido", EnvPort, p)
	}
	return ":" + strconv.Itoa(n), nil
}

// redactor junta los secretos del proceso: summary.redact.from_env.
func redactor(cfg *config.Config, getenv func(string) string) (*tasks.Redactor, error) {
	var r struct {
		FromEnv     []string `json:"from_env"`
		Replacement string   `json:"replacement"`
	}
	if err := cfg.Decode("summary.redact", &r); err != nil {
		return nil, err
	}
	var secrets []string
	for _, name := range r.FromEnv {
		secrets = append(secrets, getenv(name))
	}
	return tasks.NewRedactor(r.Replacement, secrets...), nil
}

func budgetFrom(cfg *config.Config) (tasks.Budget, error) {
	var b struct {
		Total  int `json:"task_deadline_s"`
		Margin int `json:"safety_margin_s"`
	}
	if err := cfg.Decode("budgets", &b); err != nil {
		return tasks.Budget{}, err
	}
	return tasks.Budget{Total: time.Duration(b.Total) * time.Second, Margin: time.Duration(b.Margin) * time.Second}, nil
}
