// Command rfbprobe opera el escritorio a mano: conecta por RFB, ejecuta
// primitivas encadenadas y guarda capturas. Sirve para depurar y para relevar
// coordenadas y datos del corpus.
//
//	rfbprobe [-config dir] [-timeout 15s] comando [args] [comando [args] ...]
//
// Comandos:
//
//	shot ARCHIVO.png|.jpg     captura tras esperar pantalla estable
//	click X Y                 clic izquierdo y espera de estabilidad
//	dclick X Y                doble clic
//	hover X Y                 mover sin botones (hover_dwell_ms) y esperar
//	type 'TEXTO'              escribe en el foco actual sin borrar antes
//	replace X Y 'TEXTO'       reemplaza el contenido del campo en (X,Y)
//	key COMBO                 tecla o combinacion: enter, ctrl+a, shift+tab
//	scroll X Y DIR N          DIR = up|down|left|right, N pasos
//	zoom X Y W H [ARCHIVO]    recorte ampliado por perception.zoom.factor (zoom.png)
//	reset                     POST /__reset con SO_DESKTOP_TOKEN; imprime el codigo HTTP
//	check ARCHIVO.json        compara una region con una imagen de referencia
//
// Entorno: SO_DESKTOP_URL, SO_DESKTOP_PASSWORD, SO_DESKTOP_TOKEN, SO_CONFIG_DIR.
// Ningun mensaje incluye la contrasena ni el token.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "image/jpeg"

	"screenoperator/internal/config"
	"screenoperator/internal/rfb"
	"screenoperator/internal/screen"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// arity es cuantos argumentos fijos toma cada comando.
var arity = map[string]int{
	"shot": 1, "click": 2, "dclick": 2, "hover": 2, "type": 1, "replace": 3,
	"key": 1, "scroll": 4, "zoom": 4, "reset": 0, "check": 1,
}

type step struct {
	name string
	args []string
}

func parseSteps(args []string) ([]step, error) {
	var out []step
	for i := 0; i < len(args); {
		name := args[i]
		n, ok := arity[name]
		if !ok {
			return nil, fmt.Errorf("comando desconocido %q", name)
		}
		if i+1+n > len(args) {
			return nil, fmt.Errorf("%s necesita %d argumentos", name, n)
		}
		st := step{name: name, args: args[i+1 : i+1+n]}
		i += 1 + n
		if name == "zoom" && i < len(args) {
			if _, cmd := arity[args[i]]; !cmd && hasImageExt(args[i]) {
				st.args = append(st.args, args[i])
				i++
			}
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		return nil, errors.New("falta al menos un comando")
	}
	return out, nil
}

func hasImageExt(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

func needsScreen(steps []step) bool {
	for _, s := range steps {
		if s.name != "reset" {
			return true
		}
	}
	return false
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rfbprobe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgDir := fs.String("config", firstNonEmpty(getenv("SO_CONFIG_DIR"), "config"), "directorio de configuracion")
	timeout := fs.Duration("timeout", 15*time.Second, "timeout de conexion")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	url, password, token := getenv("SO_DESKTOP_URL"), getenv("SO_DESKTOP_PASSWORD"), getenv("SO_DESKTOP_TOKEN")
	fail := func(code int, err error) int {
		msg := err.Error()
		for _, secret := range []string{password, token} {
			if secret != "" {
				msg = strings.ReplaceAll(msg, secret, "[redactado]")
			}
		}
		fmt.Fprintln(stderr, "rfbprobe:", msg)
		return code
	}
	steps, err := parseSteps(fs.Args())
	if err != nil {
		return fail(2, err)
	}
	if url == "" {
		return fail(2, errors.New("falta SO_DESKTOP_URL"))
	}
	ctx := context.Background()
	p := &probe{url: url, token: token, out: stdout}
	if needsScreen(steps) {
		l, err := config.NewLoader(*cfgDir, getenv)
		if err != nil {
			return fail(2, err)
		}
		c, err := l.ForTask(url, "")
		if err != nil {
			return fail(2, err)
		}
		surf, perc, err := screen.FromConfig(c)
		if err != nil {
			return fail(2, err)
		}
		d, err := screen.Open(ctx, url, password, surf, *timeout)
		if err != nil {
			return fail(1, fmt.Errorf("conexion al escritorio: %w", err))
		}
		defer d.Close()
		w, h := d.Size()
		fmt.Fprintf(stdout, "conectado: %dx%d %q\n", w, h, d.Name())
		p.ex = screen.NewExecutor(d, surf, perc)
		p.perc = perc
	}
	for _, s := range steps {
		if err := p.do(ctx, s); err != nil {
			return fail(1, fmt.Errorf("%s: %w", s.name, err))
		}
	}
	return 0
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

type probe struct {
	url, token string
	out        io.Writer
	ex         *screen.Executor
	perc       screen.Perception
}

func ints(args []string, n int) ([]int, error) {
	out := make([]int, n)
	for i := 0; i < n; i++ {
		v, err := strconv.Atoi(args[i])
		if err != nil {
			return nil, fmt.Errorf("%q no es un entero", args[i])
		}
		out[i] = v
	}
	return out, nil
}

func (p *probe) report(what string, o screen.Outcome) {
	fmt.Fprintf(p.out, "%s: estable=%v cambio=%v espera=%v total=%v\n",
		what, o.Stable, o.Changed, o.Settle.Round(time.Millisecond), o.Duration.Round(time.Millisecond))
}

func (p *probe) do(ctx context.Context, s step) error {
	switch s.name {
	case "reset":
		code, err := rfb.Reset(ctx, nil, p.url, p.token)
		if err != nil {
			return err
		}
		fmt.Fprintf(p.out, "reset: HTTP %d\n", code)
		if code/100 != 2 {
			return fmt.Errorf("el escritorio respondio HTTP %d", code)
		}
		return nil
	case "shot":
		o, err := p.ex.Screenshot(ctx)
		if err != nil {
			return err
		}
		if err := saveImage(s.args[0], o.Screen, p.perc); err != nil {
			return err
		}
		b := o.Screen.Bounds()
		fmt.Fprintf(p.out, "shot: %s %dx%d estable=%v\n", s.args[0], b.Dx(), b.Dy(), o.Stable)
		return nil
	case "click", "dclick", "hover":
		xy, err := ints(s.args, 2)
		if err != nil {
			return err
		}
		f := map[string]func(context.Context, int, int) (screen.Outcome, error){
			"click": p.ex.Click, "dclick": p.ex.DoubleClick, "hover": p.ex.Hover,
		}[s.name]
		o, err := f(ctx, xy[0], xy[1])
		if err != nil {
			return err
		}
		p.report(fmt.Sprintf("%s (%d,%d)", s.name, xy[0], xy[1]), o)
		return nil
	case "type":
		o, err := p.ex.Type(ctx, s.args[0])
		if err != nil {
			return err
		}
		p.report(fmt.Sprintf("type (%d caracteres)", len([]rune(s.args[0]))), o)
		return nil
	case "replace":
		xy, err := ints(s.args, 2)
		if err != nil {
			return err
		}
		o, err := p.ex.TypeReplace(ctx, xy[0], xy[1], s.args[2])
		if err != nil {
			return err
		}
		p.report(fmt.Sprintf("replace (%d,%d)", xy[0], xy[1]), o)
		return nil
	case "key":
		o, err := p.ex.Key(ctx, s.args[0])
		if err != nil {
			return err
		}
		p.report("key "+s.args[0], o)
		return nil
	case "scroll":
		xy, err := ints(s.args, 2)
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(s.args[3])
		if err != nil {
			return fmt.Errorf("%q no es un entero", s.args[3])
		}
		o, err := p.ex.Scroll(ctx, xy[0], xy[1], s.args[2], n)
		if err != nil {
			return err
		}
		p.report(fmt.Sprintf("scroll (%d,%d) %s %d", xy[0], xy[1], s.args[2], n), o)
		return nil
	case "zoom":
		v, err := ints(s.args, 4)
		if err != nil {
			return err
		}
		img, err := p.ex.Zoom(ctx, image.Rect(v[0], v[1], v[0]+v[2], v[1]+v[3]))
		if err != nil {
			return err
		}
		file := "zoom.png"
		if len(s.args) > 4 {
			file = s.args[4]
		}
		if err := saveImage(file, img, p.perc); err != nil {
			return err
		}
		fmt.Fprintf(p.out, "zoom: %s %dx%d\n", file, img.Bounds().Dx(), img.Bounds().Dy())
		return nil
	case "check":
		return p.check(ctx, s.args[0])
	}
	return fmt.Errorf("comando %q sin implementar", s.name)
}

func saveImage(path string, img image.Image, perc screen.Perception) error {
	var b []byte
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		q := perc.Screenshot.JPEGQuality
		if q == 0 {
			q = 80
		}
		b, err = screen.EncodeJPEG(img, q)
	default:
		b, err = screen.EncodePNG(img)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// checkSpec es el formato de `check archivo.json`:
//
//	{"region": [x, y, w, h], "reference": "ref.png", "max_diff": 0.02}
//
// reference es relativa al JSON. Sale con error si la fraccion de pixeles
// distintos supera max_diff (0.01 por defecto).
type checkSpec struct {
	Region    []int    `json:"region"`
	Reference string   `json:"reference"`
	MaxDiff   *float64 `json:"max_diff"`
}

func (p *probe) check(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var spec checkSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(spec.Region) != 4 || spec.Reference == "" {
		return fmt.Errorf("%s: se espera region [x,y,w,h] y reference", path)
	}
	maxDiff := 0.01
	if spec.MaxDiff != nil {
		maxDiff = *spec.MaxDiff
	}
	ref := spec.Reference
	if !filepath.IsAbs(ref) {
		ref = filepath.Join(filepath.Dir(path), ref)
	}
	f, err := os.Open(ref)
	if err != nil {
		return err
	}
	defer f.Close()
	refImg, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	o, err := p.ex.Screenshot(ctx)
	if err != nil {
		return err
	}
	r := spec.Region
	got, err := screen.Zoom(o.Screen, image.Rect(r[0], r[1], r[0]+r[2], r[1]+r[3]), 1)
	if err != nil {
		return err
	}
	want := image.NewRGBA(image.Rect(0, 0, refImg.Bounds().Dx(), refImg.Bounds().Dy()))
	for y := 0; y < want.Rect.Dy(); y++ {
		for x := 0; x < want.Rect.Dx(); x++ {
			want.Set(x, y, refImg.At(refImg.Bounds().Min.X+x, refImg.Bounds().Min.Y+y))
		}
	}
	d := screen.DiffFraction(got, want)
	fmt.Fprintf(p.out, "check: %s diferencia=%.4f max=%.4f\n", path, d, maxDiff)
	if d > maxDiff {
		return fmt.Errorf("la region difiere de %s en %.2f%% de los pixeles", ref, d*100)
	}
	return nil
}
