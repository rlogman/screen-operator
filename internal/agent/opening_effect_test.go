package agent

import (
	"context"
	"image"
	"strings"
	"testing"

	"screenoperator/internal/fakes"
)

// decideCase es un dialogo y el boton que debe elegir Decide ("" = ninguno:
// con efecto, la tarea termina fallida sin tocar nada).
type decideCase struct {
	d      Dialog
	effect bool
	label  string
}

func checkDecide(t *testing.T, p DialogPolicy, cases []decideCase) {
	t.Helper()
	for _, c := range cases {
		dec := p.Decide(c.d)
		got := ""
		if dec.Button != nil {
			got = dec.Button.Label
		}
		if dec.Effect != c.effect || got != c.label {
			t.Errorf("%q / %q %v: efecto=%v boton=%q; se esperaba efecto=%v boton=%q", c.d.Title, c.d.Text, c.d.Buttons, dec.Effect, got, c.effect, c.label)
		}
	}
}

func btns(labels ...string) []DialogButton {
	out := make([]DialogButton, len(labels))
	for i, l := range labels {
		out[i] = DialogButton{Label: l}
	}
	return out
}

// runOpeningDialog corre una tarea cuyo primer vistazo muestra el dialogo
// jsonDialog; safe es la region del boton seguro (vacia = no hay).
func runOpeningDialog(t *testing.T, p DialogPolicy, jsonDialog string, safe image.Rectangle) (*fakes.Driver, Outcome) {
	t.Helper()
	d := modalDriver(safe)
	replies := []string{jsonClassifyEdit, jsonDialog}
	if !safe.Empty() {
		replies = append(replies, jsonOpeningNoDialog, jsonOrient, `{"action":"finish","estado":"fallida","razon":"prueba"}`)
	}
	chat := fakes.NewScriptedLLM(replies...)
	a, _ := newTestAgent(t, chat, d, 3)
	a.Cfg.Dialogs = p
	out := a.Run(context.Background(), "Corrige la dirección de X: ahora es Y.")
	return d, out
}

func expectFailedUntouched(t *testing.T, d *fakes.Driver, out Outcome, name string) {
	t.Helper()
	if out.Done || !strings.Contains(out.Summary, name) || !strings.Contains(out.Summary, "no se confirmó") {
		t.Fatalf("se esperaba fallida nombrando el dialogo %q; %+v", name, out)
	}
	if ev := d.Events(); len(ev) != 0 {
		t.Fatalf("no debia tocar el escritorio; eventos %v", ev)
	}
}

var cancelRect = image.Rect(600, 470, 680, 494) // boton en (640,483)

func expectOneClickOn(t *testing.T, d *fakes.Driver, r image.Rectangle) {
	t.Helper()
	if cs := clicks(d); len(cs) != 1 || !cs[0].In(r) {
		t.Fatalf("se esperaba un unico clic en el boton seguro %v; hubo %v", r, cs)
	}
}

// Validador it. 1, punto 1: un boton que repite el verbo del dialogo
// ("¿Cerrar sesión?" [Cerrar, Cancelar]) confirma la accion; nunca es seguro.
func TestE10_1_S2_ButtonEchoingDialogVerbIsNeverSafe(t *testing.T) {
	p := dialogPolicy(t)
	checkDecide(t, p, []decideCase{
		{Dialog{Title: "¿Cerrar sesión?", Kind: "confirmacion", Buttons: btns("Cerrar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "¿Cerrar caja?", Kind: "confirmacion", Buttons: btns("Cerrar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "¿Cerrar período?", Kind: "confirmacion", Buttons: btns("Cerrar", "Cancelar")}, true, "Cancelar"},
		// Aunque el modelo lo lea informativo, y aunque Cerrar este en cancel_labels.
		{Dialog{Title: "¿Cerrar sesión?", Kind: "informativo", Buttons: btns("Cerrar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "¿Cerrar sesión?", Kind: "informativo", Buttons: btns("Cerrar")}, true, ""},
		{Dialog{Title: "Cierre de caja", Text: "Se cerrará la caja del día.", Kind: "informativo", Buttons: btns("Cerrar", "Volver")}, true, "Volver"},
		{Dialog{Title: "¿Cerrar período?", Kind: "confirmacion", Buttons: btns("Cerrar", "x")}, true, "x"},
		{Dialog{Title: "¿Cancelar la factura FV-12?", Kind: "confirmacion", Buttons: btns("Cancelar", "Volver")}, true, "Volver"},
		{Dialog{Title: "¿Cancelar el pedido?", Kind: "confirmacion", Buttons: btns("Cancelar")}, true, ""},
		// Un boton Cerrar que NO es el verbo del dialogo sigue siendo seguro.
		{Dialog{Title: "¿Eliminar el cliente?", Kind: "confirmacion", Buttons: btns("Eliminar", "Cerrar")}, true, "Cerrar"},
	})

	// De punta a punta, leido como informativo: un unico clic, en Cancelar.
	d, _ := runOpeningDialog(t, p, `{"titulo_app":"App","version":null,"dialogo":{"titulo":"¿Cerrar sesión?","texto":"Se cerrará su sesión.","tipo":"informativo","botones":[{"texto":"Cerrar","x":760,"y":483},{"texto":"Cancelar","x":640,"y":483}]}}`, cancelRect)
	expectOneClickOn(t, d, cancelRect)

	// Sin opcion segura: fallida nombrando el dialogo, sin tocar el escritorio.
	d2, out := runOpeningDialog(t, p, `{"titulo_app":"App","version":null,"dialogo":{"titulo":"¿Cerrar caja?","texto":"","tipo":"informativo","botones":[{"texto":"Cerrar","x":760,"y":483}]}}`, image.Rectangle{})
	expectFailedUntouched(t, d2, out, "¿Cerrar caja?")
}

// Validador it. 1, punto 2: el texto del dialogo y la forma de sus botones
// suben el riesgo aunque el modelo lo lea informativo; nunca lo bajan.
func TestE10_1_S2_DialogTextAndButtonsRaiseRisk(t *testing.T) {
	p := dialogPolicy(t)
	for _, v := range []string{"eliminar", "borrar", "anular", "cancelar"} {
		if !inLabels(v, p.DestructiveVerbs) {
			t.Fatalf("policy.destructive.keywords: falta el verbo %q en %v", v, p.DestructiveVerbs)
		}
	}
	for _, q := range []string{"todos", "todas"} {
		if inLabels(q, p.DestructiveVerbs) {
			t.Fatalf("%q es cuantificador, no verbo: %v", q, p.DestructiveVerbs)
		}
	}
	checkDecide(t, p, []decideCase{
		{Dialog{Title: "Atención", Text: "Se eliminará el registro.", Kind: "informativo", Buttons: btns("Aceptar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "Atención", Text: "Se borrarán 30 facturas.", Kind: "informativo", Buttons: btns("Continuar")}, true, ""},
		{Dialog{Title: "Aviso", Text: "La factura quedará anulada.", Kind: "informativo", Buttons: btns("Entendido")}, true, ""},
		// Cancelar/No + otro boton = forma de confirmacion, aunque el texto sea neutro.
		{Dialog{Title: "Aviso", Text: "¿Desea continuar?", Kind: "informativo", Buttons: btns("Aceptar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "Aviso", Kind: "informativo", Buttons: btns("Sí", "No")}, true, "No"},
		// Un texto con "no" no vuelve inseguro al boton No.
		{Dialog{Title: "¿Desea anular?", Text: "Esta acción no se puede deshacer.", Kind: "confirmacion", Buttons: btns("Sí", "No")}, true, "No"},
	})

	// [Continuar] ante "Se borrarán 30 facturas", leido como informativo:
	// fallida sin tocar el escritorio.
	d, out := runOpeningDialog(t, p, `{"titulo_app":"App","version":null,"dialogo":{"titulo":"Atención","texto":"Se borrarán 30 facturas.","tipo":"informativo","botones":[{"texto":"Continuar","x":760,"y":483}]}}`, image.Rectangle{})
	expectFailedUntouched(t, d, out, "Atención")

	// "Atención: se eliminará el registro" [Aceptar, Cancelar] leido como
	// informativo: un unico clic, en Cancelar.
	d2, _ := runOpeningDialog(t, p, `{"titulo_app":"App","version":null,"dialogo":{"titulo":"Atención","texto":"Se eliminará el registro.","tipo":"informativo","botones":[{"texto":"Aceptar","x":760,"y":483},{"texto":"Cancelar","x":640,"y":483}]}}`, cancelRect)
	expectOneClickOn(t, d2, cancelRect)
}

// Los casos que ya salian bien siguen saliendo bien.
func TestE10_1_DialogPolicyDecideRegressions(t *testing.T) {
	p := dialogPolicy(t)
	checkDecide(t, p, []decideCase{
		{Dialog{Title: "¿Eliminar registro?", Kind: "confirmacion", Buttons: btns("Aceptar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "Eliminar", Kind: "confirmacion", Buttons: btns("Eliminar", "Cancelar")}, true, "Cancelar"},
		{Dialog{Title: "¿Desea anular?", Kind: "confirmacion", Buttons: btns("Sí", "No")}, true, "No"},
		{Dialog{Title: "Pago", Kind: "confirmacion", Buttons: btns("Confirmar pago")}, true, ""},
		{Dialog{Title: "Novedades de la versión 4.2", Text: "Actualizamos el sistema. Los formularios ahora se aplican sin cerrarse.", Kind: "informativo", Buttons: btns("x", "Entendido")}, false, "Entendido"},
		{Dialog{Title: "Novedades de la versión 4.2", Kind: "informativo", Buttons: btns("Entendido")}, false, "Entendido"},
		{Dialog{Title: "Novedades", Kind: "informativo", Buttons: btns("Cerrar")}, false, "Cerrar"},
	})
}
