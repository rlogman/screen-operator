package agent

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"screenoperator/internal/fakes"
	"screenoperator/internal/screen"
)

// halfView es una captura de 1280x800 reducida a 640x400 para el modelo.
func halfView() View {
	full := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	return FullView(screen.ForModel(full, 640))
}

func TestE6_2_S1_ClickImageToScreen(t *testing.T) {
	a, err := Parse(`{"action":"click","x":120,"y":45,"reason":"abrir menú"}`, halfView())
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != KindClick || a.ImgX != 120 || a.ImgY != 45 || a.Reason != "abrir menú" {
		t.Fatalf("accion = %+v", a)
	}
	// La imagen del modelo mide la mitad: la pantalla es el doble.
	if a.X != 240 || a.Y != 90 {
		t.Fatalf("pantalla = (%d,%d), se esperaba (240,90)", a.X, a.Y)
	}
	// Sin reduccion, imagen = pantalla; con cercas de markdown tambien se acepta.
	full := FullView(screen.ForModel(image.NewRGBA(image.Rect(0, 0, 1280, 800)), 1280))
	a, err = Parse("```json\n{\"action\":\"click\",\"x\":120,\"y\":45,\"reason\":\"abrir menú\"}\n```", full)
	if err != nil || a.X != 120 || a.Y != 45 {
		t.Fatalf("sin reduccion: %+v %v", a, err)
	}
	// Desde un zoom x2 de la region (100,200)-(300,300): (40,10) -> (120,205).
	zv := ZoomView(image.NewRGBA(image.Rect(0, 0, 400, 200)), image.Rect(100, 200, 300, 300), 1280, 800)
	a, err = Parse(`{"action":"click","x":40,"y":10}`, zv)
	if err != nil || a.X != 120 || a.Y != 205 {
		t.Fatalf("desde zoom: %+v %v", a, err)
	}
}

func TestE6_2_S2_OutOfImageNamesField(t *testing.T) {
	v := halfView() // 640x400
	cases := map[string]string{
		`{"action":"click","x":700,"y":45}`:                  "x",
		`{"action":"click","x":-1,"y":45}`:                   "x",
		`{"action":"double_click","x":10,"y":400}`:           "y",
		`{"action":"click","y":45}`:                          "x",
		`{"action":"zoom","x":600,"y":10,"w":100,"h":50}`:    "w",
		`{"action":"scroll","x":1,"y":1,"direction":"diag"}`: "direction",
		`{"action":"type","text":"hola"}`:                    "x",
		`{"action":"commit","x":1,"y":1,"what":"Guardar"}`:   "target",
		`{"action":"click","x":1,"y":1,"pensamiento":"..."}`: "pensamiento",
		`{"action":"volar"}`:                                 "action",
		`{"action":"finish","estado":"quizas","razon":"x"}`:  "estado",
	}
	for in, field := range cases {
		_, err := Parse(in, v)
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Field != field {
			t.Fatalf("%s: err = %v, se esperaba error del campo %q", in, err, field)
		}
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("%s: el mensaje %q no nombra el campo", in, err)
		}
	}
}

func TestE6_2_S3_RepairOnceThenStepError(t *testing.T) {
	// (a) Salida no JSON, reparacion valida: el paso sigue con la accion reparada.
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient,
		"voy a hacer clic en el menu",      // no es JSON
		`{"action":"click","x":10,"y":10}`, // reparacion
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, _ := newTestAgent(t, chat, gray(), 10)
	out := a.Run(context.Background(), "Corrige la dirección de X")
	calls := chat.Calls()
	if len(calls) != 5 {
		t.Fatalf("llamadas = %d, se esperaban 5", len(calls))
	}
	rep := calls[3]
	if !strings.Contains(requestText(rep), "no es valida") || requestImages(rep) != 1 {
		t.Fatalf("la reparacion no pide corregir o no trae exactamente una imagen: %d imagenes", requestImages(rep))
	}
	if out.State.Steps != 2 || len(out.State.History) == 0 || !strings.Contains(out.State.History[0], "click(10,10)") {
		t.Fatalf("historial = %v", out.State.History)
	}

	// (b) Dos salidas invalidas seguidas: una sola reparacion y el paso cuenta como error.
	chat = fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient, "nada", "tampoco",
		`{"action":"finish","estado":"fallida","razon":"prueba"}`)
	a, _ = newTestAgent(t, chat, gray(), 10)
	out = a.Run(context.Background(), "Corrige la dirección de X")
	if n := len(chat.Calls()); n != 5 {
		t.Fatalf("llamadas = %d, se esperaban 5 (una reparacion, no dos)", n)
	}
	if out.State.Steps != 2 || !strings.Contains(out.State.History[0], "respuesta invalida") || !strings.Contains(out.State.History[0], "error") {
		t.Fatalf("el paso invalido no cuenta como error: pasos %d historial %v", out.State.Steps, out.State.History)
	}
}

func TestE6_2_S4_CommitTarget(t *testing.T) {
	a, err := Parse(`{"action":"commit","x":600,"y":700,"what":"Guardar cliente CL-0007","target":"CL-0007"}`,
		FullView(screen.ForModel(image.NewRGBA(image.Rect(0, 0, 1280, 800)), 1280)))
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != KindCommit || a.Target != "CL-0007" || a.What != "Guardar cliente CL-0007" || a.X != 600 || a.Y != 700 {
		t.Fatalf("accion = %+v", a)
	}
}

func TestE6_2_S5_FinishIsARequest(t *testing.T) {
	a, err := Parse(`{"action":"finish","estado":"terminada","datos":["45"],"razon":"conteo hecho"}`, halfView())
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != KindFinish || a.Finish == nil || a.Finish.Status != StatusDone || len(a.Finish.Data) != 1 || a.Finish.Data[0] != "45" || a.Finish.Reason != "conteo hecho" {
		t.Fatalf("finish = %+v", a.Finish)
	}
	// La solicitud no fija el estado final: sin verificar, el loop no la acepta.
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"action":"finish","estado":"terminada","datos":["45"],"razon":"conteo hecho"}`,
		`{"action":"finish","estado":"fallida","razon":"no pude confirmar"}`)
	ag, _ := newTestAgent(t, chat, gray(), 10)
	out := ag.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	if out.Done {
		t.Fatalf("la primera solicitud 'terminada' se acepto sin verificar: %q", out.Summary)
	}
}
