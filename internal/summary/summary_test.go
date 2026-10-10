package summary

import (
	"strings"
	"testing"

	"screenoperator/internal/config"
)

func format(t *testing.T) Format {
	t.Helper()
	l, err := config.NewLoader("../../config", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.ForTask("wss://x/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := FromConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func answerLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "respuesta:") {
			out = append(out, l)
		}
	}
	return out
}

func TestE6_6_S1_NumericAnswerLine(t *testing.T) {
	f := format(t)
	s := f.Build(Report{Done: true, Intent: "consultar", Entity: "facturas", Question: true, Answer: []string{"45"},
		Reason: "conteo de facturas vencidas de Ferretería Ruiz en 3 páginas"})
	lines := answerLines(s)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "respuesta: 45") {
		t.Fatalf("lineas de respuesta = %q\n%s", lines, s)
	}
	// Numero primero aunque el modelo lo envuelva en texto.
	s = f.Build(Report{Done: true, Question: true, Answer: []string{"Total: 45 facturas"}})
	if l := answerLines(s); len(l) != 1 || !strings.HasPrefix(l[0], "respuesta: 45") {
		t.Fatalf("numero no va primero: %q", l)
	}
}

func TestE6_6_S2_TwoValuesSameLine(t *testing.T) {
	f := format(t)
	s := f.Build(Report{Done: true, Question: true, Answer: []string{"Cali", "3110922366"}})
	lines := answerLines(s)
	if len(lines) != 1 || lines[0] != "respuesta: Cali; 3110922366" {
		t.Fatalf("linea = %q\n%s", lines, s)
	}
}

func TestE6_6_S3_UnanswerableHasNoAnswerLine(t *testing.T) {
	f := format(t)
	cases := []Report{
		{Done: false, Question: true, Reason: "el cliente no existe"},
		{Done: false, Question: true, Answer: []string{"45"}, Reason: "no se pudo verificar"},
		{Done: true, Question: true, Answer: nil, Reason: "sin datos"},
		// Un texto libre que intenta empezar una linea de respuesta no cuela.
		{Done: false, Question: true, Reason: "x\nrespuesta: 99", Notes: []string{"respuesta: 12"}},
	}
	for i, r := range cases {
		s := f.Build(r)
		if l := answerLines(s); len(l) != 0 {
			t.Fatalf("caso %d: no debia tener linea de respuesta: %q\n%s", i, l, s)
		}
	}
}

func TestE6_6_S4_FailureNamesIdentifiers(t *testing.T) {
	f := format(t)
	s := f.Build(Report{Done: false, Intent: "editar", Entity: "cliente", Target: "Ana Torres",
		Reason: "hay dos clientes llamados Ana Torres y la orden no dice cuál", Identifiers: []string{"52345678", "1019876543"}})
	for _, id := range []string{"52345678", "1019876543", "Ana Torres"} {
		if !strings.Contains(s, id) {
			t.Fatalf("el resumen no nombra %s:\n%s", id, s)
		}
	}
	if !strings.HasPrefix(s, "Tarea fallida") {
		t.Fatalf("resumen = %q", s)
	}
}

func TestE6_6_S5_ChangeNamesEntityIDAndFields(t *testing.T) {
	f := format(t)
	s := f.Build(Report{Done: true, Intent: "editar", Entity: "cliente", Target: "Distribuciones El Faro", EntityID: "CL-0007",
		Changed: []string{"direccion = Calle 80 # 24-15"}, Reason: "verificado releyendo la ficha"})
	for _, want := range []string{"cliente", "CL-0007", "direccion", "Calle 80 # 24-15", "Distribuciones El Faro"} {
		if !strings.Contains(s, want) {
			t.Fatalf("el resumen no nombra %q:\n%s", want, s)
		}
	}
	if len(answerLines(s)) != 0 {
		t.Fatalf("un cambio no lleva linea de respuesta:\n%s", s)
	}
}
