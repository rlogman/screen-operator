package agent

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"screenoperator/internal/config"
	"screenoperator/internal/fakes"
)

// dialogPolicy es la politica de config/base.yaml (policy.dialogs).
func dialogPolicy(t *testing.T) DialogPolicy {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.ForTask("wss://x/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := DialogPolicyFromConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.MaxOnOpen == 0 {
		t.Fatalf("policy.dialogs deberia estar habilitado en base.yaml: %+v", p)
	}
	return p
}

// modalDriver simula una app con un modal: el fondo es gris y el modal un
// rectangulo blanco; un clic dentro de closeRect lo cierra (la pantalla cambia).
func modalDriver(closeRect image.Rectangle) *fakes.Driver {
	d := fakes.NewDriver(1280, 800, color.RGBA{128, 128, 128, 255})
	d.Fill(image.Rect(380, 296, 900, 503), color.RGBA{250, 250, 250, 255})
	d.OnClick = func(dd *fakes.Driver, x, y int) {
		if (image.Point{x, y}).In(closeRect) {
			dd.Fill(image.Rect(0, 0, 1280, 800), color.RGBA{220, 220, 220, 255})
		}
	}
	return d
}

func clicks(d *fakes.Driver) []image.Point {
	var out []image.Point
	var last uint8
	for _, e := range d.Events() {
		if e.Pointer {
			if last&1 == 1 && e.Buttons&1 == 0 {
				out = append(out, image.Pt(e.X, e.Y))
			}
			last = e.Buttons
		}
	}
	return out
}

const (
	jsonOpeningNoDialog = `{"titulo_app":"App de prueba v4.2","version":"v4.2","dialogo":null}`
	jsonOpeningInfo     = `{"titulo_app":"App de prueba v4.2","version":"v4.2","dialogo":{"titulo":"Novedades de la versión 4.2","texto":"Actualizamos el sistema. Los formularios ahora se aplican sin cerrarse.","tipo":"informativo","botones":[{"texto":"x","x":878,"y":310},{"texto":"Entendido","x":840,"y":483}]}}`
)

func TestE10_1_S1_InformativeDialogClosedBeforeAnyAction(t *testing.T) {
	d := modalDriver(image.Rect(795, 472, 886, 494))
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOpeningInfo, jsonOpeningNoDialog, jsonOrient,
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, tr := newTestAgent(t, chat, d, 3)
	a.Cfg.Dialogs = dialogPolicy(t)
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	if out.Done {
		t.Fatalf("el guion termina fallida: %s", out.Summary)
	}
	// La primera (y unica) accion sobre el escritorio es el clic en "Entendido"
	// (se prefiere un boton con texto a la x de la esquina).
	cs := clicks(d)
	if len(cs) != 1 {
		t.Fatalf("se esperaba exactamente un clic (cerrar el dialogo); hubo %v", cs)
	}
	if p := cs[0]; !p.In(image.Rect(795, 472, 886, 494)) {
		t.Fatalf("el clic %v no cae en el boton Entendido", p)
	}
	// Orden: clasificar, apertura (dialogo), apertura (libre), orientar, actuar.
	var purposes []string
	for _, r := range chat.Requests {
		purposes = append(purposes, r.Purpose)
	}
	if strings.Join(purposes[:4], ",") != "clasificar,apertura,apertura,orientar" {
		t.Fatalf("orden de llamadas %v", purposes)
	}
	var closed, free bool
	for _, s := range tr.steps {
		if s.Phase == "orientar" && strings.Contains(s.Reason, "diálogo informativo cerrado") && strings.Contains(s.Reason, "Novedades") {
			closed = true
		}
		if s.Phase == "orientar" && strings.Contains(s.Reason, "libre de dialogos") {
			free = true
		}
	}
	if !closed || !free {
		t.Fatalf("la traza no registra 'diálogo informativo cerrado' y la pantalla libre: %+v", tr.steps)
	}
	// El modelo del loop ve en el historial que el dialogo se cerro.
	if last := requestText(chat.Requests[4]); !strings.Contains(last, "diálogo informativo cerrado") {
		t.Fatalf("el historial del loop no menciona el dialogo cerrado:\n%s", last)
	}
}

func TestE10_1_S2_EffectDialogIsCancelledNeverConfirmed(t *testing.T) {
	// Con opcion segura: se cancela, nunca se pulsa 'Eliminar'.
	cancel := image.Rect(600, 470, 680, 494)
	d := modalDriver(cancel)
	jsonEffect := `{"titulo_app":"App","version":null,"dialogo":{"titulo":"¿Eliminar el cliente CL-0001?","texto":"Esta acción no se puede deshacer.","tipo":"confirmacion","botones":[{"texto":"Eliminar","x":760,"y":483},{"texto":"Cancelar","x":640,"y":483}]}}`
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonEffect, jsonOpeningNoDialog, jsonOrient,
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, tr := newTestAgent(t, chat, d, 3)
	a.Cfg.Dialogs = dialogPolicy(t)
	a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	cs := clicks(d)
	if len(cs) != 1 || !cs[0].In(cancel) {
		t.Fatalf("se esperaba un unico clic en 'Cancelar'; hubo %v", cs)
	}
	found := false
	for _, s := range tr.steps {
		if strings.Contains(s.Reason, "cancelado sin confirmar") {
			found = true
		}
	}
	if !found {
		t.Fatalf("la traza no registra la cancelacion: %+v", tr.steps)
	}

	// Sin opcion segura ('Confirmar pago' y 'Aceptar cambios' son efecto): la tarea
	// termina fallida nombrando el dialogo, sin tocar el escritorio.
	for _, buttons := range []string{
		`[{"texto":"Confirmar pago","x":760,"y":483}]`,
		`[{"texto":"Aceptar cambios","x":760,"y":483},{"texto":"Aceptar","x":660,"y":483}]`,
	} {
		d2 := modalDriver(image.Rectangle{})
		jsonNoSafe := `{"titulo_app":"App","version":null,"dialogo":{"titulo":"Pago pendiente","texto":"Hay un pago por confirmar.","tipo":"informativo","botones":` + buttons + `}}`
		chat2 := fakes.NewScriptedLLM(jsonClassifyEdit, jsonNoSafe)
		a2, _ := newTestAgent(t, chat2, d2, 3)
		a2.Cfg.Dialogs = dialogPolicy(t)
		out := a2.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
		if out.Done {
			t.Fatalf("%s: se esperaba fallida", buttons)
		}
		if !strings.Contains(out.Summary, "Pago pendiente") || !strings.Contains(out.Summary, "no se confirmó") {
			t.Fatalf("%s: el resumen no nombra el dialogo: %q", buttons, out.Summary)
		}
		if ev := d2.Events(); len(ev) != 0 {
			t.Fatalf("%s: no debia tocar el escritorio; eventos %v", buttons, ev)
		}
		if n := len(chat2.Requests); n != 2 {
			t.Fatalf("%s: tras el dialogo sin opcion segura no debe haber mas llamadas; hubo %d", buttons, n)
		}
	}
}

func TestE10_1_DialogPolicyDecide(t *testing.T) {
	p := dialogPolicy(t)
	cases := []struct {
		d      Dialog
		effect bool
		label  string
	}{
		{Dialog{Title: "Novedades de la versión 4.2", Kind: "informativo", Buttons: []DialogButton{{Label: "Entendido"}}}, false, "Entendido"},
		{Dialog{Title: "Bienvenido", Kind: "informativo", Buttons: []DialogButton{{Label: "Aceptar"}}}, false, "Aceptar"},
		{Dialog{Title: "Aviso", Kind: "informativo", Buttons: []DialogButton{{Label: "Ir al módulo"}}}, false, "Ir al módulo"},
		{Dialog{Title: "¿Eliminar registro?", Kind: "confirmacion", Buttons: []DialogButton{{Label: "Aceptar"}, {Label: "Cancelar"}}}, true, "Cancelar"},
		{Dialog{Title: "Cambios sin guardar", Kind: "informativo", Buttons: []DialogButton{{Label: "Aceptar cambios"}, {Label: "×"}}}, true, "×"},
		{Dialog{Title: "Confirmar pago", Kind: "confirmacion", Buttons: []DialogButton{{Label: "Sí"}, {Label: "No"}}}, true, "No"},
		{Dialog{Title: "Pago", Kind: "confirmacion", Buttons: []DialogButton{{Label: "Confirmar pago"}}}, true, ""},
	}
	for _, c := range cases {
		dec := p.Decide(c.d)
		got := ""
		if dec.Button != nil {
			got = dec.Button.Label
		}
		if dec.Effect != c.effect || got != c.label {
			t.Errorf("%q: efecto=%v boton=%q; se esperaba efecto=%v boton=%q", c.d.Title, dec.Effect, got, c.effect, c.label)
		}
	}
}

// appLoader arma un directorio de configuracion con la base del repo y un
// perfil de app propio.
func appLoader(t *testing.T, profile string) *config.Loader {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"surfaces", "apps"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"base.yaml", "schema.json", "surfaces/vnc-kiosk.yaml"} {
		b, err := os.ReadFile(filepath.Join("../../config", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if profile != "" {
		if err := os.WriteFile(filepath.Join(dir, "apps", "sigecom.yaml"), []byte(profile), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := config.NewLoader(dir, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.CheckAll(); err != nil {
		t.Fatal(err)
	}
	return l
}

const profileV39 = `app:
  id: sigecom
  match:
    screen_text_any: [SIGECOM]
    version: v3.9
  quirks:
    - "PISTA-V39: Guardar persiste, Volver descarta."
`

func TestE10_2_S1_VersionMismatchDiscardsProfileHints(t *testing.T) {
	r := &Runner{Loader: appLoader(t, profileV39)}
	d := fakes.NewDriverFrom(loadFixture(t, "sigecom_home.png"))
	chat := fakes.NewScriptedLLM(jsonClassifyEdit,
		`{"titulo_app":"SIGECOM · Distribuidora Andina v4.2","version":"v4.2","dialogo":null}`,
		`{"hoy":"Hoy: 14/10/2026","formato_fecha":"DD/MM/AAAA","titulo":"SIGECOM · Distribuidora Andina v4.2","texto_visible":"Bienvenido a SIGECOM","menus":["Archivo","Comercial"]}`,
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, tr := newTestAgent(t, chat, d, 3)
	a.Cfg.Dialogs = dialogPolicy(t)
	a.AppResolve = r.appResolve
	var logBuf bytes.Buffer
	a.Log = slog.New(slog.NewJSONHandler(&logBuf, nil))
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	const want = "cambio de app detectado: v3.9 -> v4.2"
	if out.State.AppProfile != "" {
		t.Fatalf("con la version distinta no debe aplicarse el perfil; aplicado %q", out.State.AppProfile)
	}
	inTrace := false
	for _, s := range tr.steps {
		if s.Phase == "orientar" && strings.Contains(s.Reason, want) {
			inTrace = true
		}
	}
	if !inTrace {
		t.Fatalf("la traza no registra %q: %+v", want, tr.steps)
	}
	if !strings.Contains(logBuf.String(), want) {
		t.Fatalf("el log no registra %q:\n%s", want, logBuf.String())
	}
	// Modo generico: el paso siguiente no lleva las pistas del perfil.
	last := requestText(chat.Requests[len(chat.Requests)-1])
	if strings.Contains(last, "PISTAS DE LA APP") || strings.Contains(last, "PISTA-V39") {
		t.Fatalf("las pistas del perfil v3.9 llegaron al modelo:\n%s", last)
	}

	// En config: la seleccion por texto (epica 1) sigue eligiendo el perfil,
	// pero la huella lo descarta.
	res, err := r.Loader.ResolveAppFingerprint("SIGECOM · Distribuidora Andina v4.2")
	if err != nil || res.OK || res.Change != want || res.Layer.ID != "sigecom" {
		t.Fatalf("ResolveAppFingerprint = %+v, %v", res, err)
	}
}

func TestE10_2_S2_V42ProfilePassesStrictValidation(t *testing.T) {
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	// Validacion estricta de todas las combinaciones (lo mismo que al arrancar).
	if _, apps, err := l.CheckAll(); err != nil || apps == 0 {
		t.Fatalf("CheckAll: apps=%d err=%v", apps, err)
	}
	as, err := l.AppProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	for _, a := range as {
		if a.ID == "sigecom" {
			tree = a.Tree
		}
	}
	app, _ := tree["app"].(map[string]any)
	match, _ := app["match"].(map[string]any)
	if match["version"] != "v4.2" {
		t.Fatalf("config/apps/sigecom.yaml deberia esperar v4.2; app.match.version = %v", match["version"])
	}
	hints := RenderHints(tree)
	for _, label := range []string{"Entendido", "Filtrar", "Crear cliente", "Aplicar"} {
		if !strings.Contains(hints, label) {
			t.Errorf("las pistas v4.2 no mencionan la etiqueta nueva %q", label)
		}
	}
	// Un typo o una version mal formada en el perfil se rechazan.
	bad := strings.Replace(profileV39, "version: v3.9", "version: tres", 1)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"base.yaml", "schema.json"} {
		b, _ := os.ReadFile(filepath.Join("../../config", f))
		_ = os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	_ = os.MkdirAll(filepath.Join(dir, "surfaces"), 0o755)
	b, _ := os.ReadFile("../../config/surfaces/vnc-kiosk.yaml")
	_ = os.WriteFile(filepath.Join(dir, "surfaces", "vnc-kiosk.yaml"), b, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "apps", "x.yaml"), []byte(bad), 0o644)
	lb, err := config.NewLoader(dir, func(string) string { return "" })
	if err == nil {
		_, _, err = lb.CheckAll()
	}
	if err == nil {
		t.Fatal("un perfil con app.match.version invalida deberia rechazarse")
	}
}

func TestE10_2_S3_MatchingVersionUsesProfileHints(t *testing.T) {
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Loader: l}
	d := fakes.NewDriverFrom(loadFixture(t, "sigecom_home.png"))
	chat := fakes.NewScriptedLLM(jsonClassifyEdit,
		`{"titulo_app":"SIGECOM · Distribuidora Andina v4.2","version":"v4.2","dialogo":null}`,
		`{"hoy":"Hoy: 14/10/2026","formato_fecha":"DD/MM/AAAA","titulo":"SIGECOM · Distribuidora Andina","texto_visible":"Bienvenido a SIGECOM","menus":["Archivo"]}`,
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, tr := newTestAgent(t, chat, d, 3)
	a.Cfg.Dialogs = dialogPolicy(t)
	a.AppResolve = r.appResolve
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	if out.State.AppProfile != "sigecom" {
		t.Fatalf("perfil aplicado %q, se esperaba sigecom", out.State.AppProfile)
	}
	last := requestText(chat.Requests[len(chat.Requests)-1])
	if !strings.Contains(last, "PISTAS DE LA APP") || !strings.Contains(last, "Aplicar") {
		t.Fatalf("el paso no lleva las pistas del perfil v4.2:\n%s", last)
	}
	for _, s := range tr.steps {
		if strings.Contains(s.Reason, "cambio de app detectado") {
			t.Fatalf("no debe registrar cambio de app con la version correcta: %+v", s)
		}
	}
	// La version vino de la revision de apertura (el titulo de orientar no la trae).
	res, _ := l.ResolveAppFingerprint("SIGECOM · Distribuidora Andina\nversion v4.2")
	if !res.OK || res.Seen != "v4.2" {
		t.Fatalf("huella v4.2: %+v", res)
	}
}
