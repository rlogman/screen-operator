package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"screenoperator/internal/fakes"
)

// pageJSON arma la respuesta del extractor con n facturas desde el codigo first.
func pageJSON(first, n int, next bool) string { return pageJSONInd(first, n, next, "") }

// pageJSONInd es pageJSON con indicador de pagina ("Página N de M"); "" = null.
func pageJSONInd(first, n int, next bool, ind string) string {
	type row map[string]string
	rows := []row{}
	for i := 0; i < n; i++ {
		rows = append(rows, row{"Factura": fmt.Sprintf("FV-%05d", first+i), "Cliente": "Ferretería Ruiz", "Estado": "Vencida", "Valor": "1.250.000"})
	}
	b, _ := json.Marshal(map[string]any{
		"columnas": []string{"Factura", "Cliente", "Estado", "Valor"}, "filas": rows,
		"siguiente": map[string]bool{"visible": true, "habilitado": next}, "pagina": nil,
	})
	if ind != "" {
		b, _ = json.Marshal(map[string]any{
			"columnas": []string{"Factura", "Cliente", "Estado", "Valor"}, "filas": rows,
			"siguiente": map[string]bool{"visible": true, "habilitado": next}, "pagina": ind,
		})
	}
	return string(b)
}

func mustPage(t *testing.T, s string) Page {
	t.Helper()
	p, err := ParsePage(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestE6_5_S1_CountAcrossPages(t *testing.T) {
	g := NewAggregator("facturas vencidas", "Factura", "conteo", map[string]string{"Estado": "Vencida", "Cliente": "Ferretería Ruiz"})
	// Sin indicador: un avance de pagina entre extracciones.
	g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSON(21, 20, true)))
	g.NoteAdvance()
	_, stop := g.AddPage(mustPage(t, pageJSON(41, 5, false)))
	if !stop || !g.Complete {
		t.Fatalf("la ultima pagina debia cerrar la extraccion completa: stop=%v complete=%v %q", stop, g.Complete, g.Stop)
	}
	if c := g.Count(); c != 45 {
		t.Fatalf("conteo = %d, se esperaba 45", c)
	}
	// Con indicador "Página N de M".
	g = NewAggregator("facturas vencidas", "Factura", "conteo", map[string]string{"Estado": "Vencida"})
	g.AddPage(mustPage(t, pageJSONInd(1, 20, true, "Página 1 de 3")))
	g.AddPage(mustPage(t, pageJSONInd(21, 20, true, "Página 2 de 3")))
	g.AddPage(mustPage(t, pageJSONInd(41, 5, false, "Página 3 de 3")))
	if !g.Complete || g.Count() != 45 {
		t.Fatalf("con indicador: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}

	// Extremo a extremo: el loop extrae cada pagina y el codigo pone el 45.
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"localizar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo","filters":{"Estado":"Vencida","Cliente":"Ferretería Ruiz"}}`,
		pageJSONInd(1, 20, true, "Página 1 de 3"),
		`{"fase":"localizar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"localizar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSONInd(21, 20, true, "Página 2 de 3"),
		`{"fase":"localizar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"localizar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSONInd(41, 5, false, "Página 3 de 3"),
		// El modelo cuenta mal (40): el codigo lo reemplaza.
		`{"fase":"verificar","action":"finish","estado":"terminada","datos":["40"],"agregado":"conteo","razon":"conteo terminado"}`,
		`{"fase":"verificar","action":"finish","estado":"terminada","datos":["40"],"agregado":"conteo","razon":"conteo terminado"}`,
	)
	a, _ := newTestAgent(t, chat, gray(), 20)
	out := a.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	if !out.Done {
		t.Fatalf("no termino: %q\n%v", out.Summary, out.State.History)
	}
	if !strings.HasPrefix(out.Summary, "respuesta: 45\n") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	for _, c := range chat.Calls() {
		if c.Purpose == "extraer" && c.Model != "test/extractor" {
			t.Fatalf("la extraccion debe usar el rol verifier/extractor: %s", c.Model)
		}
	}
}

func TestE6_5_S2_SamePageTwiceNoDuplicates(t *testing.T) {
	g := NewAggregator("facturas", "Factura", "conteo", nil)
	added, _ := g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	if added != 20 {
		t.Fatalf("agregadas = %d", added)
	}
	added, _ = g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	if added != 0 || g.Count() != 20 {
		t.Fatalf("segunda extraccion de la misma pagina: agregadas %d, total %d", added, g.Count())
	}
	// La clave se compara sin distinguir mayusculas ni espacios extremos.
	p := mustPage(t, `{"filas":[{"factura":" fv-00001 ","Estado":"Vencida"}],"siguiente":{"visible":true,"habilitado":true}}`)
	if added, _ := g.AddPage(p); added != 0 {
		t.Fatal("la misma factura con otro formato se conto dos veces")
	}
}

func TestE6_5_S3_StopOnDisabledNextOrRepeatedPage(t *testing.T) {
	g := NewAggregator("facturas", "Factura", "conteo", nil)
	if _, stop := g.AddPage(mustPage(t, pageJSON(1, 20, true))); stop {
		t.Fatal("con Siguiente habilitado no debia detenerse")
	}
	g.NoteAdvance()
	if _, stop := g.AddPage(mustPage(t, pageJSON(21, 3, false))); !stop || !g.Complete || !strings.Contains(g.Stop, "siguiente") {
		t.Fatalf("Siguiente deshabilitado: stop=%v complete=%v %q", stop, g.Complete, g.Stop)
	}
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	// La app no avanzo (clic sin efecto): la pagina nueva repite la anterior.
	// Se detiene, pero NO esta completa: "siguiente" sigue habilitado.
	if _, stop := g.AddPage(mustPage(t, pageJSON(1, 20, true))); !stop || g.Complete || !strings.Contains(g.Stop, "la pagina no avanzo; reintenta siguiente") {
		t.Fatalf("pagina repetida: stop=%v complete=%v %q", stop, g.Complete, g.Stop)
	}
	// Reintentar siguiente y extraer continua normalmente.
	g.NoteAdvance()
	if _, stop := g.AddPage(mustPage(t, pageJSON(21, 3, false))); !stop || !g.Complete || g.Count() != 23 {
		t.Fatalf("tras reintentar: stop=%v complete=%v conteo=%d %q", stop, g.Complete, g.Count(), g.Stop)
	}
	// Sin boton siguiente visible tambien se detiene (lista de una pagina).
	g = NewAggregator("x", "", "datos", nil)
	if _, stop := g.AddPage(mustPage(t, `{"filas":[{"a":"1"}],"siguiente":{"visible":false,"habilitado":true}}`)); !stop || !g.Complete {
		t.Fatal("sin boton siguiente visible debia detenerse completa")
	}

	// En el loop: pagina repetida con siguiente habilitado -> cierre rechazado.
	chat0 := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSON(1, 20, true),
		`{"fase":"verificar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSON(1, 20, true),
		`{"fase":"verificar","action":"finish","estado":"terminada","agregado":"conteo","razon":"listo"}`,
		`{"action":"finish","estado":"fallida","razon":"no pude paginar"}`)
	a0, _ := newTestAgent(t, chat0, gray(), 10)
	out0 := a0.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	if h := strings.Join(out0.State.History, "\n"); out0.Done || !strings.Contains(h, "no aceptado: la extraccion no esta completa: la pagina no avanzo; reintenta siguiente") {
		t.Fatalf("aceptó un conteo sobre una pagina que no avanzo: %q %v", out0.Summary, out0.State.History)
	}

	// En el loop: un finish con agregado antes de terminar las paginas no se acepta.
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSON(1, 20, true),
		`{"fase":"verificar","action":"finish","estado":"terminada","agregado":"conteo","razon":"listo"}`,
		`{"action":"finish","estado":"fallida","razon":"no pude paginar"}`)
	a, _ := newTestAgent(t, chat, gray(), 10)
	out := a.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	if out.Done || !strings.Contains(strings.Join(out.State.History, "\n"), "no esta completa") {
		t.Fatalf("aceptó un conteo con paginas pendientes: %q %v", out.Summary, out.State.History)
	}
}

func TestE6_5_S4_ThousandsSeparator(t *testing.T) {
	cases := map[string]float64{
		"6.980.000":        6980000,
		"$ 6.980.000":      6980000,
		"45.000":           45000,
		"1.234,50":         1234.5,
		"12,5":             12.5,
		"980":              980,
		"COP 2.000.000,00": 2000000,
		"-1.500":           -1500,
	}
	for in, want := range cases {
		got, err := ParseAmount(in)
		if err != nil || got != want {
			t.Errorf("%q = %v %v, se esperaba %v", in, got, err, want)
		}
	}
	g := NewAggregator("facturas", "Factura", "suma", nil)
	g.AddPage(mustPage(t, `{"filas":[{"Factura":"A","Valor":"6.980.000"},{"Factura":"B","Valor":"20.000"}],"siguiente":{"visible":false}}`))
	s, err := g.Sum("valor")
	if err != nil || FormatNumber(s) != "7000000" {
		t.Fatalf("suma = %v %v", s, err)
	}
}

// Regresion del falso exito E3 = 27: se extrajo la pagina 1, dos clics
// seguidos en "siguiente" (la 2 nunca se extrajo) y luego la 3 de 3 con
// "siguiente" deshabilitado. La extraccion se detiene pero no esta completa y
// el cierre se rechaza.
func TestE6_5_S3_SkippedPageIsIncomplete(t *testing.T) {
	g := NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONInd(1, 20, true, "Página 1 de 3")))
	g.NoteAdvance()
	g.NoteAdvance()
	_, stop := g.AddPage(mustPage(t, pageJSONInd(41, 7, false, "Página 3 de 3")))
	if !stop || g.Complete || !strings.Contains(g.Stop, "faltan las paginas 2 de 3") {
		t.Fatalf("pagina saltada con indicador: stop=%v complete=%v %q", stop, g.Complete, g.Stop)
	}
	// Volver a la 2 y extraerla completa la lista (en cualquier orden).
	g.AddPage(mustPage(t, pageJSONInd(21, 20, true, "Página 2 de 3")))
	if !g.Complete || g.Count() != 47 {
		t.Fatalf("tras extraer la 2: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}

	// Sin indicador: dos avances entre extracciones no es confiable.
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	g.NoteAdvance()
	g.NoteAdvance()
	if _, stop := g.AddPage(mustPage(t, pageJSON(41, 7, false))); !stop || g.Complete || !strings.Contains(g.Stop, "2 avances") {
		t.Fatalf("pagina saltada sin indicador: stop=%v complete=%v %q", stop, g.Complete, g.Stop)
	}
	// El acumulado se reinicia y una pasada limpia desde la primera completa.
	g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSON(21, 20, true)))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSON(41, 7, false)))
	if !g.Complete || g.Count() != 47 {
		t.Fatalf("pasada limpia: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}

	// En el loop: el finish con agregado se rechaza y nunca sale "respuesta: 27".
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSONInd(1, 20, true, "Página 1 de 3"),
		`{"fase":"verificar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"verificar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSONInd(41, 7, false, "Página 3 de 3"),
		`{"fase":"verificar","action":"finish","estado":"terminada","agregado":"conteo","razon":"extraccion completa"}`,
		`{"action":"finish","estado":"fallida","razon":"no pude completar"}`)
	a, _ := newTestAgent(t, chat, gray(), 12)
	out := a.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	h := strings.Join(out.State.History, "\n")
	if out.Done || strings.Contains(out.Summary, "respuesta:") || !strings.Contains(h, "no aceptado: la extraccion no esta completa: faltan las paginas 2 de 3") {
		t.Fatalf("aceptó un conteo con una pagina saltada: %q\n%s", out.Summary, h)
	}
}

// Una pregunta de conteo con extraccion no acepta un numero puesto a ojo.
func TestE6_5_S1_CountWithoutAggregateIsRejected(t *testing.T) {
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSONInd(1, 5, false, "Página 1 de 1"),
		`{"fase":"verificar","action":"finish","estado":"terminada","datos":["27"],"razon":"conte a ojo"}`,
		`{"fase":"verificar","action":"finish","estado":"terminada","datos":["27"],"agregado":"conteo","razon":"conteo en codigo"}`)
	a, _ := newTestAgent(t, chat, gray(), 10)
	out := a.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	h := strings.Join(out.State.History, "\n")
	if !strings.Contains(h, "no aceptado: extrajiste con purpose \"conteo\"") {
		t.Fatalf("aceptó un conteo a ojo: %q\n%s", out.Summary, h)
	}
	if !out.Done || !strings.HasPrefix(out.Summary, "respuesta: 5\n") {
		t.Fatalf("resumen = %q\n%s", out.Summary, h)
	}
}
