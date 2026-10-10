package guard

import (
	"context"
	"strings"
	"testing"
)

func TestE7_5_S1_DesktopPasswordRedacted(t *testing.T) {
	g := ForTask(func(string) string { return "" })(nil, []string{"P4ss-xyz"}).(*Guard)
	got := g.Redact("Tarea fallida: la clave del escritorio P4ss-xyz fue rechazada.")
	if strings.Contains(got, "P4ss-xyz") || !strings.Contains(got, "[redactado]") {
		t.Fatalf("resumen = %q", got)
	}
}

func TestE7_5_S2_ScreenSecretByLabelRedacted(t *testing.T) {
	g := New().(*Guard)
	s := st("Revisa la integración contable", "consultar", "", nil)
	g.ObservePage(s, extract("datos", "Campo", nil), page([]string{"Campo", "Valor"},
		[]string{"Sistema", "Contable XYZ"}, []string{"Clave de integración", "SGC-XXXX-YYYY"}))
	got := g.Redact("Tarea terminada. Leído: SGC-XXXX-YYYY en Integraciones.")
	if strings.Contains(got, "SGC-XXXX-YYYY") || !strings.Contains(got, "[redactado]") {
		t.Fatalf("resumen = %q", got)
	}
	if !strings.Contains(g.Redact("Sistema: Contable XYZ"), "Contable XYZ") {
		t.Fatal("redactó un valor sin etiqueta sensible")
	}
	// Un valor como columna con etiqueta sensible, y un par etiqueta: valor en el texto.
	g.ObservePage(s, extract("datos", "Usuario", nil), page([]string{"Usuario", "Token"}, []string{"admin", "tok_ABCDEF123"}))
	if strings.Contains(g.Redact("el token es tok_ABCDEF123"), "tok_ABCDEF123") {
		t.Fatal("no redactó la columna Token")
	}
	if got := g.Redact("Contraseña: hunter22"); strings.Contains(got, "hunter22") {
		t.Fatalf("par etiqueta: valor sin redactar: %q", got)
	}
	// Extremo a extremo: el resumen final pasa por el redactor.
	chat := &routerLLM{script: []string{
		`{"intencion":"consultar","entidad":"integracion","objetivo":"","valores":{},"pregunta":true,"datos_pedidos":["sistema"],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"leer"}`,
		orient,
		`{"fase":"localizar","action":"extract","what":"ficha","key":"Campo","purpose":"datos"}`,
		`{"columnas":["Campo","Valor"],"filas":[["Sistema","Contable XYZ"],["Clave de integración","SGC-XXXX-YYYY"]],"siguiente":{"visible":false,"habilitado":false},"pagina":null}`,
		`{"fase":"verificar","action":"finish","estado":"terminada","datos":["Contable XYZ"],"razon":"leido junto a SGC-XXXX-YYYY"}`,
	}}
	a, _ := newAgent(t, chat, gray())
	out := a.Run(context.Background(), "¿Qué sistema está integrado?")
	if strings.Contains(out.Summary, "SGC-XXXX-YYYY") {
		t.Fatalf("el resumen trae el secreto: %q", out.Summary)
	}
	for _, h := range out.State.History {
		if strings.Contains(h, "SGC-XXXX-YYYY") {
			t.Fatalf("el historial trae el secreto: %q", h)
		}
	}
}

func TestE7_5_S3_EnvTokensRedacted(t *testing.T) {
	env := map[string]string{"TEAM_TOKEN": "team-tok-123456", "OPENROUTER_API_KEY": "sk-or-v1-abcdef"}
	g := ForTask(func(k string) string { return env[k] })(testConfig(t), nil).(*Guard)
	got := g.Redact("token team-tok-123456 y clave sk-or-v1-abcdef")
	if strings.Contains(got, "team-tok-123456") || strings.Contains(got, "sk-or-v1-abcdef") {
		t.Fatalf("resumen = %q", got)
	}
}

func TestE7_5_S4_CleanSummaryUnchanged(t *testing.T) {
	env := map[string]string{"TEAM_TOKEN": "team-tok-123456"}
	g := ForTask(func(k string) string { return env[k] })(testConfig(t), []string{"P4ss-xyz"}).(*Guard)
	in := "respuesta: 45\nTarea terminada: consultar factura Ferretería Ruiz.\nRazón: conteo en código.\nIdentificadores: CL-0005, 52345678."
	if got := g.Redact(in); got != in {
		t.Fatalf("cambió un resumen sin secretos:\n%q\n%q", in, got)
	}
}
