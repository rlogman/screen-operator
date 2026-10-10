package agent

import (
	"bytes"
	"context"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/fakes"
	"screenoperator/internal/llm"
)

func TestE6_4_S1_AppDateFromSigecomFixture(t *testing.T) {
	d := fakes.NewDriverFrom(loadFixture(t, "sigecom_home.png"))
	chat := fakes.NewScriptedLLM(jsonClassifyEdit)
	// El modelo lee la barra superior de la captura; el codigo interpreta la fecha.
	chat.Push(fakes.Reply{Func: func(r llm.Request) (string, error) {
		img := r.Messages[len(r.Messages)-1].Parts[1].Image
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
		if err != nil || cfg.Width != 1280 || cfg.Height != 800 {
			t.Errorf("orientar no recibio la captura de 1280x800: %v %+v", err, cfg)
		}
		return `{"hoy":"Hoy: 14/10/2026 · Usuario: operador2","formato_fecha":"DD/MM/AAAA","titulo":"SIGECOM · Distribuidora Andina v3.9","texto_visible":"Bienvenido a SIGECOM","menus":["Archivo","Comercial","Cartera","Herramientas","Ayuda"]}`, nil
	}})
	chat.Push(fakes.Reply{Content: `{"action":"finish","estado":"fallida","razon":"prueba"}`})
	a, tr := newTestAgent(t, chat, d, 5)
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	if got := out.State.Today.Format("2006-01-02"); got != "2026-10-14" || out.State.TodayFrom != "app" {
		t.Fatalf("fecha de la app = %s (%s)", got, out.State.TodayFrom)
	}
	found := false
	for _, s := range tr.steps {
		if s.Phase == "orientar" && strings.Contains(s.Reason, "2026-10-14") {
			found = true
		}
	}
	if !found {
		t.Fatalf("la traza de orientar no registra la fecha: %+v", tr.steps)
	}
	// Las fechas de la orden se cuentan desde ahi: el siguiente paso las ve resueltas.
	last := requestText(chat.Calls()[2])
	if !strings.Contains(last, "2026-10-14 (miércoles, fuente: app)") {
		t.Fatalf("el paso no recibe la fecha de la app:\n%s", last)
	}
}

func TestE6_4_S2_RelativeDates(t *testing.T) {
	today := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) // miercoles
	if WeekdayES(today) != "miércoles" {
		t.Fatalf("2026-10-14 es %s", WeekdayES(today))
	}
	cases := map[string]string{
		"ayer":                "2026-10-13",
		"el sábado":           "2026-10-10",
		"el sabado":           "2026-10-10",
		"la semana pasada":    "2026-10-05 a 2026-10-11",
		"septiembre de 2026":  "2026-09-01 a 2026-09-30",
		"hoy":                 "2026-10-14",
		"anteayer":            "2026-10-12",
		"hace 3 días":         "2026-10-11",
		"esta semana":         "2026-10-12 a 2026-10-14",
		"el mes pasado":       "2026-09-01 a 2026-09-30",
		"el miércoles":        "2026-10-07",
		"febrero":             "2026-02-01 a 2026-02-28",
		"en septiembre 2026":  "2026-09-01 a 2026-09-30",
		"14/10/2026":          "2026-10-14",
		"el año pasado":       "2025-01-01 a 2025-12-31",
		"el domingo pasado":   "2026-10-11",
		"La Semana Pasada.":   "2026-10-05 a 2026-10-11",
		"diciembre de 2025":   "2025-12-01 a 2025-12-31",
		"este mes":            "2026-10-01 a 2026-10-14",
		"el lunes":            "2026-10-12",
		"mañana":              "2026-10-15",
		"septiembre del 2026": "2026-09-01 a 2026-09-30",
	}
	for expr, want := range cases {
		r, ok := ResolveRelative(today, expr)
		if !ok || r.String() != want {
			t.Errorf("%q = %v (%v), se esperaba %s", expr, r, ok, want)
		}
	}
	if _, ok := ResolveRelative(today, "cuando llueva"); ok {
		t.Error("una expresion desconocida no debe resolverse")
	}
	// Lectura de la fecha en formatos distintos.
	for in, want := range map[string]string{
		"Hoy: 14/10/2026":         "2026-10-14",
		"Fecha 2026-10-14":        "2026-10-14",
		"14 de octubre de 2026":   "2026-10-14",
		"Today 10/14/2026":        "2026-10-14",
		"Hoy: 31/02/2026 (error)": "",
	} {
		format := ""
		if strings.HasPrefix(in, "Today") {
			format = "MM/DD/AAAA"
		}
		got, err := ParseAppDate(in, format)
		if want == "" {
			if err == nil {
				t.Errorf("%q debia fallar", in)
			}
			continue
		}
		if err != nil || got.Format("2006-01-02") != want {
			t.Errorf("%q = %v %v", in, got, err)
		}
	}
}

func TestE6_4_S3_NoAppDateUsesServerDate(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyEdit,
		`{"hoy":null,"formato_fecha":"","titulo":"Otra app","texto_visible":"Inventario","menus":["Archivo"]}`,
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, tr := newTestAgent(t, chat, gray(), 5)
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	if out.State.TodayFrom != "servidor" || out.State.Today.Format("2006-01-02") != "2026-10-10" {
		t.Fatalf("fecha = %v (%s), se esperaba la del servidor 2026-10-10", out.State.Today, out.State.TodayFrom)
	}
	var note string
	for _, s := range tr.steps {
		if s.Phase == "orientar" {
			note = s.Reason
		}
	}
	if !strings.Contains(note, "fecha del servidor") {
		t.Fatalf("la nota de la traza no indica la fecha del servidor: %q", note)
	}
}
