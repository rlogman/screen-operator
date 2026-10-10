package agent

import (
	"context"
	"image"
	"image/color"
	"reflect"
	"strings"
	"testing"

	"screenoperator/internal/fakes"
	"screenoperator/internal/llm"
)

// editScript es una edicion completa guionada: localizar, actuar (escribir y
// guardar), verificar y terminar.
func editScript() *fakes.ScriptedLLM {
	return fakes.NewScriptedLLM(
		jsonClassifyEdit,
		jsonOrient,
		`{"fase":"localizar","action":"click","x":100,"y":40,"reason":"abrir menu de clientes","nota":"el cliente es CL-0007"}`,
		`{"fase":"localizar","action":"double_click","x":200,"y":300,"reason":"abrir la ficha de Distribuciones El Faro"}`,
		`{"fase":"actuar","action":"type","x":400,"y":350,"text":"Calle 80 # 24-15","replace":true,"reason":"nueva direccion"}`,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente CL-0007","target":"CL-0007"}`,
		`{"fase":"verificar","action":"double_click","x":200,"y":300,"reason":"reabrir la ficha"}`,
		`{"action":"finish","estado":"terminada","razon":"direccion guardada","entidad":"cliente","id_registro":"CL-0007","cambios":["direccion = Calle 80 # 24-15"],"verificado":true,"evidencia":"la ficha muestra Calle 80 # 24-15"}`,
	)
}

func TestE6_3_S1_PhasesInOrderAndTraced(t *testing.T) {
	d := gray()
	d.OnClick = func(d *fakes.Driver, x, y int) { d.Fill(image.Rect(0, 0, 50, 50), color.RGBA{uint8(x), uint8(y), 0, 255}) }
	chat := editScript()
	a, tr := newTestAgent(t, chat, d, 20)
	out := a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	if !out.Done {
		t.Fatalf("no termino: %q\nhistorial %v", out.Summary, out.State.History)
	}
	want := []string{"clasificar", "orientar", "localizar", "actuar", "verificar", "reportar"}
	if got := tr.phases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fases en la traza = %v, se esperaba %v", got, want)
	}
	gotPhases := make([]string, len(out.State.Phases))
	for i, p := range out.State.Phases {
		gotPhases[i] = string(p)
	}
	if !reflect.DeepEqual(gotPhases, want) {
		t.Fatalf("fases recorridas = %v", gotPhases)
	}
	for _, s := range tr.steps {
		if s.Phase == "" {
			t.Fatalf("paso sin fase: %+v", s)
		}
	}
	if !strings.Contains(out.Summary, "CL-0007") || !strings.Contains(out.Summary, "Calle 80 # 24-15") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	if len(out.State.Commits) != 1 || out.State.Commits[0].Target != "CL-0007" {
		t.Fatalf("commits = %+v", out.State.Commits)
	}
}

func TestE6_3_S2_RequestHasContextAndOneImage(t *testing.T) {
	chat := editScript()
	a, _ := newTestAgent(t, chat, gray(), 20)
	instr := "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15."
	a.Run(context.Background(), instr)
	calls := chat.Calls()
	if len(calls) < 6 {
		t.Fatalf("llamadas = %d", len(calls))
	}
	for i, r := range calls {
		if n := requestImages(r); n != 1 {
			t.Fatalf("request %d (%s) trae %d imagenes, se esperaba exactamente 1", i, r.Purpose, n)
		}
		txt := requestText(r)
		for _, want := range []string{"ORDEN: " + instr, "FECHA DE LA APP", "NOTAS:", "HISTORIAL"} {
			if !strings.Contains(txt, want) {
				t.Fatalf("request %d (%s) no contiene %q", i, r.Purpose, want)
			}
		}
	}
	// Desde el primer paso de accion: la fecha de la app ya leida, las notas y
	// el historial en texto con las acciones anteriores.
	last := requestText(calls[len(calls)-1])
	for _, want := range []string{"2026-10-14", "el cliente es CL-0007", "commit(600,700", "double_click(200,300)"} {
		if !strings.Contains(last, want) {
			t.Fatalf("el ultimo request no contiene %q:\n%s", want, last)
		}
	}
}

func TestE6_3_S3_StepLimit(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient)
	chat.Default = &fakes.Reply{Content: `{"fase":"localizar","action":"scroll","x":600,"y":400,"direction":"down","amount":1}`}
	a, _ := newTestAgent(t, chat, gray(), 5)
	out := a.Run(context.Background(), "Corrige la dirección de X")
	if out.Done {
		t.Fatal("termino sin finish")
	}
	if out.State.Steps != 5 {
		t.Fatalf("pasos = %d, se esperaban 5", out.State.Steps)
	}
	if len(chat.Calls()) != 2+5 {
		t.Fatalf("llamadas = %d, se esperaban 7", len(chat.Calls()))
	}
	if !strings.Contains(out.Summary, "límite de pasos") || !strings.HasPrefix(out.Summary, "Tarea fallida") {
		t.Fatalf("resumen = %q", out.Summary)
	}
}

func TestE6_3_S4_FinishWithoutVerifyRunsVerification(t *testing.T) {
	chat := fakes.NewScriptedLLM(
		jsonClassifyEdit, jsonOrient,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente CL-0007","target":"CL-0007"}`,
		// Pide terminada sin pasar por verificar (aunque diga fase verificar).
		`{"fase":"verificar","action":"finish","estado":"terminada","razon":"guardado","verificado":true}`,
		// El loop entro a verificar: el modelo relee y vuelve a pedir el cierre.
		`{"fase":"verificar","action":"double_click","x":200,"y":300,"reason":"reabrir la ficha"}`,
		`{"action":"finish","estado":"terminada","razon":"guardado","entidad":"cliente","id_registro":"CL-0007","cambios":["direccion = Calle 80 # 24-15"],"verificado":true,"evidencia":"se lee Calle 80 # 24-15"}`,
	)
	a, tr := newTestAgent(t, chat, gray(), 20)
	out := a.Run(context.Background(), "Corrige la dirección de Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	if !out.Done {
		t.Fatalf("no termino: %q %v", out.Summary, out.State.History)
	}
	calls := chat.Calls()
	if len(calls) != 6 {
		t.Fatalf("llamadas = %d, se esperaban 6", len(calls))
	}
	if txt := requestText(calls[4]); !strings.Contains(txt, "FASE ACTUAL: verificar") || !strings.Contains(txt, "VERIFICA") {
		t.Fatalf("tras el primer finish el loop no paso a verificar:\n%s", txt)
	}
	if !strings.Contains(strings.Join(out.State.History, "\n"), "falta verificar") {
		t.Fatalf("historial = %v", out.State.History)
	}
	want := []string{"clasificar", "orientar", "actuar", "verificar", "reportar"}
	if got := tr.phases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fases = %v", got)
	}

	// Si la verificacion no confirma (verificado=false), el estado es fallida.
	chat = fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar","target":"CL-0007"}`,
		`{"action":"finish","estado":"terminada","razon":"guardado"}`,
		`{"action":"finish","estado":"terminada","razon":"guardado","verificado":false,"evidencia":"la ficha sigue con la direccion vieja"}`)
	a, _ = newTestAgent(t, chat, gray(), 20)
	out = a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	if out.Done || !strings.Contains(out.Summary, "no se pudo verificar") {
		t.Fatalf("sin verificacion debia ser fallida: %q", out.Summary)
	}
}

func TestE6_3_S5_RejectTouchesNothing(t *testing.T) {
	d := gray()
	chat := fakes.NewScriptedLLM(`{"intencion":"rechazar","entidad":"cliente","objetivo":"todos los clientes inactivos","valores":{},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"cambio masivo: eliminar todos los clientes inactivos","plan":""}`)
	a, tr := newTestAgent(t, chat, d, 20)
	out := a.Run(context.Background(), "Elimina todos los clientes inactivos")
	if out.Done {
		t.Fatal("una orden rechazada no puede terminar")
	}
	if ev := d.Events(); len(ev) != 0 {
		t.Fatalf("el driver recibio %d eventos", len(ev))
	}
	if len(chat.Calls()) != 1 {
		t.Fatalf("llamadas = %d, solo se esperaba la clasificacion", len(chat.Calls()))
	}
	if !strings.Contains(out.Summary, "cambio masivo") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	if got := tr.phases(); !reflect.DeepEqual(got, []string{"clasificar", "reportar"}) {
		t.Fatalf("fases = %v", got)
	}
}

// Arrastre de so-epic-2: con Stable=false el loop vuelve a capturar y no
// decide sobre una captura no estable.
func TestLoopDoesNotDecideOnUnstableCapture(t *testing.T) {
	d := gray()
	flicker := 0
	stopFlicker := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopFlicker:
				return
			default:
			}
			flicker++
			d.Fill(image.Rect(0, 0, 400, 400), color.RGBA{uint8(flicker), uint8(flicker >> 8), uint8(flicker >> 16), 255})
		}
	}()
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient)
	chat.Default = &fakes.Reply{Func: func(llm.Request) (string, error) {
		return `{"action":"finish","estado":"fallida","razon":"x"}`, nil
	}}
	a, _ := newTestAgent(t, chat, d, 3)
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	close(stopFlicker)
	// Con la pantalla cambiando siempre, el loop gasta pasos esperando y nunca
	// pregunta al modelo por una accion.
	if n := len(chat.Calls()); n != 2 {
		t.Fatalf("el modelo decidio sobre capturas no estables: %d llamadas", n)
	}
	if len(d.Events()) != 0 {
		t.Fatal("se enviaron eventos sobre una pantalla no estable")
	}
	if !strings.Contains(strings.Join(out.State.History, "\n"), "no se estabilizo") {
		t.Fatalf("historial = %v", out.State.History)
	}
}
