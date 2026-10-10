package agent

// Tests del escalamiento de modelo (historia 8.2): con LLM falso, el rol de
// escalamiento decide solo cuando el loop se traba y nunca mas del maximo.

import (
	"context"
	"image"
	"image/color"
	"testing"

	"screenoperator/internal/fakes"
	"screenoperator/internal/llm"
)

const escModel = "test/escalation"

func withEscalation(a *Agent, pol llm.Escalation) {
	a.Cfg.Escalation = llm.Role{Model: escModel, MaxTokens: 700}
	a.Cfg.EscalationPolicy = pol
}

// actModels devuelve el modelo de cada llamada "actuar" (sin reparaciones).
func actModels(chat *fakes.ScriptedLLM) []string {
	var out []string
	for _, r := range chat.Calls() {
		if r.Purpose == "actuar" {
			out = append(out, r.Model)
		}
	}
	return out
}

func countModel(ms []string, m string) int {
	n := 0
	for _, x := range ms {
		if x == m {
			n++
		}
	}
	return n
}

const (
	jsonClickSame = `{"fase":"localizar","action":"click","x":100,"y":40,"reason":"abrir el desplegable de ciudad"}`
	jsonGiveUp    = `{"action":"finish","estado":"fallida","razon":"no se encontro el registro","identificadores":[]}`
)

// changingDriver cambia la pantalla en cada clic (la accion siempre tiene efecto).
func changingDriver() *fakes.Driver {
	d := gray()
	n := 0
	d.OnClick = func(d *fakes.Driver, x, y int) {
		n++
		d.Fill(image.Rect(0, 0, 60, 60), color.RGBA{uint8(n * 37), uint8(n * 11), uint8(x), 255})
	}
	return d
}

func TestE8_2_Escalation_FiresOnRepeatedNoEffect(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient, jsonClickSame, jsonClickSame, jsonGiveUp)
	a, _ := newTestAgent(t, chat, gray(), 10)
	withEscalation(a, llm.Escalation{Triggers: []string{llm.TriggerNoEffect}, MaxSteps: 3})
	out := a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	got := actModels(chat)
	if len(got) < 3 {
		t.Fatalf("decisiones = %v; historial %v", got, out.State.History)
	}
	if got[0] != "test/actor" || got[1] != "test/actor" || got[2] != escModel {
		t.Fatalf("modelos por decision = %v; la tercera (tras dos clics sin efecto) debia ser %s", got, escModel)
	}
	if len(out.State.Escalated) != 1 || out.State.Escalated[0] != "3:sin_efecto" {
		t.Fatalf("Escalated = %v", out.State.Escalated)
	}
}

func TestE8_2_Escalation_FiresOnStallAndOnRejectedFinish(t *testing.T) {
	t.Run("sin_avance", func(t *testing.T) {
		chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient)
		chat.Default = &fakes.Reply{Content: jsonClickSame}
		a, _ := newTestAgent(t, chat, changingDriver(), 8)
		withEscalation(a, llm.Escalation{Triggers: []string{llm.TriggerStall}, StallSteps: 3, MaxSteps: 5})
		out := a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
		got := actModels(chat)
		// Paso 1 entra a localizar; con N=3 escala en los pasos 4 y 7.
		want := map[int]bool{3: true, 6: true}
		for i, m := range got {
			if (m == escModel) != want[i] {
				t.Fatalf("modelos por decision = %v (Escalated %v)", got, out.State.Escalated)
			}
		}
	})
	t.Run("cierre_rechazado", func(t *testing.T) {
		finishCount := `{"fase":"verificar","action":"finish","estado":"terminada","razon":"conte las facturas","datos":["5"],"verificado":true,"evidencia":"la lista muestra 5 filas"}`
		// El primer finish pasa a verificar; el segundo se rechaza (conteo a ojo).
		chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient, finishCount, finishCount)
		chat.Default = &fakes.Reply{Content: jsonGiveUp}
		a, _ := newTestAgent(t, chat, gray(), 6)
		withEscalation(a, llm.Escalation{Triggers: []string{llm.TriggerFinishRejected}, MaxSteps: 1})
		out := a.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
		got := actModels(chat)
		if len(got) < 3 || got[0] != "test/actor" || got[1] != "test/actor" || got[2] != escModel {
			t.Fatalf("modelos por decision = %v; historial %v", got, out.State.History)
		}
		if countModel(got, escModel) != 1 {
			t.Fatalf("escalados = %d, max 1: %v", countModel(got, escModel), got)
		}
	})
}

func TestE8_2_Escalation_NotOnFluidTask(t *testing.T) {
	d := gray()
	d.OnClick = func(d *fakes.Driver, x, y int) {
		d.Fill(image.Rect(0, 0, 50, 50), color.RGBA{uint8(x), uint8(y), 0, 255})
	}
	chat := editScript()
	a, _ := newTestAgent(t, chat, d, 20)
	withEscalation(a, llm.Escalation{StallSteps: 6, MaxSteps: 3}) // todos los disparadores
	out := a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	if !out.Done {
		t.Fatalf("no termino: %q\n%v", out.Summary, out.State.History)
	}
	if got := actModels(chat); countModel(got, escModel) != 0 || len(out.State.Escalated) != 0 {
		t.Fatalf("una tarea fluida no debe escalar: %v %v", got, out.State.Escalated)
	}
}

func TestE8_2_Escalation_RespectsMaxAndCostCap(t *testing.T) {
	run := func(pol llm.Escalation, role bool) []string {
		chat := fakes.NewScriptedLLM(jsonClassifyEdit, jsonOrient)
		chat.Default = &fakes.Reply{Content: jsonClickSame}
		a, _ := newTestAgent(t, chat, gray(), 20)
		if role {
			withEscalation(a, pol)
		}
		a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
		return actModels(chat)
	}
	if got := run(llm.Escalation{MaxSteps: 2, StallSteps: 3}, true); countModel(got, escModel) != 2 {
		t.Fatalf("con max_steps 2 hubo %d escalados: %v", countModel(got, escModel), got)
	}
	// Cada llamada falsa cuesta US$ 0,00018: con tope 0,0001 nunca escala.
	if got := run(llm.Escalation{MaxSteps: 5, MaxTaskCostUSD: 0.0001}, true); countModel(got, escModel) != 0 {
		t.Fatalf("con el tope de costo superado hubo escalados: %v", got)
	}
	// Sin rol de escalamiento: comportamiento actual, todo con el actor.
	if got := run(llm.Escalation{}, false); countModel(got, escModel) != 0 || countModel(got, "test/actor") != len(got) {
		t.Fatalf("sin rol hubo escalados: %v", got)
	}
}
