// Command agent expone el contrato HTTP del evento y opera escritorios por VNC.
//
// Al arrancar valida toda la configuracion (SO_CONFIG_DIR, por defecto ./config,
// mas el overlay de SO_CONFIG_OVERLAY). Si falla, termina con codigo 1.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"screenoperator/internal/config"
)

// EnvConfigDir es el directorio con base.yaml, schema.json, surfaces/ y apps/.
const EnvConfigDir = "SO_CONFIG_DIR"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
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
	fmt.Fprintf(stdout, "agent: configuracion valida (%s: %d superficies, %d perfiles de app)\n", dir, ns, na)
	if *checkOnly {
		return 0
	}
	fmt.Fprintln(stderr, "agent: servidor no implementado todavia (epica 3)")
	return 2
}
