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
		"anterior": map[string]bool{"visible": true, "habilitado": first > 1},
	})
	if ind != "" {
		b, _ = json.Marshal(map[string]any{
			"columnas": []string{"Factura", "Cliente", "Estado", "Valor"}, "filas": rows,
			"siguiente": map[string]bool{"visible": true, "habilitado": next}, "pagina": ind,
			"anterior": map[string]bool{"visible": true, "habilitado": first > 1},
		})
	}
	return string(b)
}

// Tras un commit, un "fallida" pedido sin intentar verificar se rechaza una
// vez (en vivo E1 se creo el cliente y el agente cerro fallida sin releer).
// Un segundo "fallida" si se acepta: el agente puede fallar honestamente.
func TestE6_3_S4_FailedWithoutVerifyIsNudgedOnce(t *testing.T) {
	chat := fakes.NewScriptedLLM(
		jsonClassifyEdit, jsonOrient,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente CL-0007","target":"CL-0007"}`,
		`{"fase":"verificar","action":"finish","estado":"terminada","razon":"guardado","verificado":true}`,
		`{"fase":"verificar","action":"finish","estado":"fallida","razon":"no se pudo verificar el guardado"}`,
		`{"fase":"verificar","action":"double_click","x":200,"y":300,"reason":"reabrir la ficha"}`,
		`{"action":"finish","estado":"terminada","razon":"guardado","entidad":"cliente","id_registro":"CL-0007","cambios":["direccion = Calle 80 # 24-15"],"verificado":true,"evidencia":"se lee Calle 80 # 24-15"}`,
	)
	a, _ := newTestAgent(t, chat, gray(), 20)
	out := a.Run(context.Background(), "Corrige la dirección de Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	h := strings.Join(out.State.History, "\n")
	if !out.Done || !strings.Contains(h, "no aceptado: todavia no verificaste") {
		t.Fatalf("no se pidio verificar antes de aceptar fallida: %q\n%s", out.Summary, h)
	}

	chat = fakes.NewScriptedLLM(
		jsonClassifyEdit, jsonOrient,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente CL-0007","target":"CL-0007"}`,
		`{"fase":"verificar","action":"finish","estado":"terminada","razon":"guardado","verificado":true}`,
		`{"fase":"verificar","action":"finish","estado":"fallida","razon":"no se pudo verificar"}`,
		`{"fase":"verificar","action":"finish","estado":"fallida","razon":"el valor leido no coincide"}`,
	)
	a, _ = newTestAgent(t, chat, gray(), 20)
	out = a.Run(context.Background(), "Corrige la dirección de Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	if out.Done || len(chat.Calls()) != 6 {
		t.Fatalf("el segundo fallida debia aceptarse: done=%v llamadas=%d", out.Done, len(chat.Calls()))
	}
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
	if _, stop := g.AddPage(mustPage(t, `{"filas":[{"a":"1"}],"siguiente":{"visible":false,"habilitado":true},"anterior":{"visible":false}}`)); !stop || !g.Complete {
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

	// Indicador solo con numero ("Página N", como SIGECOM v4.2): el total lo
	// fija la ultima pagina (sin siguiente).
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONInd(1, 20, true, "Página 1")))
	g.AddPage(mustPage(t, pageJSONInd(21, 20, true, "Página 2")))
	g.NoteAdvance()
	g.NoteAdvance()
	if _, stop := g.AddPage(mustPage(t, pageJSONInd(61, 7, false, "Página 4"))); !stop || g.Complete || !strings.Contains(g.Stop, "faltan las paginas 3 de 4") {
		t.Fatalf("Página N sin total: stop=%v complete=%v %q", stop, g.Complete, g.Stop)
	}
	g.AddPage(mustPage(t, pageJSONInd(41, 20, true, "Página 3")))
	if !g.Complete || g.Count() != 67 {
		t.Fatalf("Página N tras volver a la 3: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}
	if n, m, ok := ParseIndicator("Pag. 7"); !ok || n != 7 || m != 0 {
		t.Fatalf("ParseIndicator(Pag. 7) = %d %d %v", n, m, ok)
	}
	if _, _, ok := ParseIndicator("45 registros"); ok {
		t.Fatal("'45 registros' no es un numero de pagina")
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
	if !strings.Contains(h, "no aceptado: la orden pide una cantidad, total o suma") {
		t.Fatalf("aceptó un conteo a ojo: %q\n%s", out.Summary, h)
	}
	if !out.Done || !strings.HasPrefix(out.Summary, "respuesta: 5\n") {
		t.Fatalf("resumen = %q\n%s", out.Summary, h)
	}
}

// pageJSONPrev arma una pagina con el estado de "anterior" explicito:
// prev = "habilitado", "deshabilitado", "ausente" o "" (el lector no lo informa).
func pageJSONPrev(first, n int, next bool, prev, ind string) string {
	rows := []map[string]string{}
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]string{"Factura": fmt.Sprintf("FV-%05d", first+i), "Estado": "Vencida", "Valor": "1.000"})
	}
	obj := map[string]any{"columnas": []string{"Factura", "Estado", "Valor"}, "filas": rows,
		"siguiente": map[string]bool{"visible": true, "habilitado": next}, "pagina": nil}
	if ind != "" {
		obj["pagina"] = ind
	}
	switch prev {
	case "habilitado":
		obj["anterior"] = map[string]bool{"visible": true, "habilitado": true}
	case "deshabilitado":
		obj["anterior"] = map[string]bool{"visible": true, "habilitado": false}
	case "ausente":
		obj["anterior"] = map[string]bool{"visible": false}
	}
	b, _ := json.Marshal(obj)
	return string(b)
}

const jsonClassifyList = `{"intencion":"consultar","entidad":"factura","objetivo":"Ferretería Ruiz","valores":{},"pregunta":true,"datos_pedidos":["facturas vencidas"],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"filtrar facturas"}`

// Punto 1 (iteracion 3): el flag de agregado es pegajoso. Ningun camino
// acepta un numero puesto a ojo cuando hubo extraccion de conteo o suma, o
// cuando la orden pide una cantidad, total o suma.
func TestE6_5_S1_StickyAggregateFlagRejectsEyeballedCount(t *testing.T) {
	cases := []struct {
		name, classify, instr string
		script                []string
	}{
		{"conteo y luego datos", jsonClassifyList, "Lista las facturas vencidas de Ferretería Ruiz", []string{
			`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
			pageJSONInd(1, 20, true, "Página 1 de 2"),
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["20"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"datos"}`,
			pageJSONInd(1, 20, true, "Página 1 de 2"),
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["20"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["20"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["20"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["20"],"razon":"a ojo"}`,
		}},
		{"purpose datos desde el inicio", jsonClassifyQuery, "¿Cuántas facturas vencidas tiene Ferretería Ruiz?", []string{
			`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"datos"}`,
			pageJSONInd(1, 20, true, "Página 1 de 2"),
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["27"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"finish","estado":"fallida","razon":"no pude contar"}`,
		}},
		{"sin ninguna extraccion", jsonClassifyQuery, "¿Cuántas facturas vencidas tiene Ferretería Ruiz?", []string{
			// El primero pasa a verificar; el segundo llega a la regla del agregado.
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["27"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"finish","estado":"terminada","datos":["27"],"razon":"a ojo"}`,
			`{"fase":"verificar","action":"finish","estado":"fallida","razon":"no pude contar"}`,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chat := fakes.NewScriptedLLM(append([]string{c.classify, jsonOrient}, c.script...)...)
			a, _ := newTestAgent(t, chat, gray(), 20)
			out := a.Run(context.Background(), c.instr)
			h := strings.Join(out.State.History, "\n")
			if out.Done || strings.Contains(out.Summary, "respuesta:") || !strings.Contains(h, "para que el codigo calcule el numero; no lo pongas a ojo") {
				t.Fatalf("aceptó un conteo a ojo: %q\n%s", out.Summary, h)
			}
			if !out.State.NeedAggregate {
				t.Fatal("el flag de agregado debia quedar activo")
			}
		})
	}

	// Sin conteo ni palabras de cantidad, un dato puntual se acepta y no lleva
	// la nota de conteo en codigo.
	chat := fakes.NewScriptedLLM(jsonClassifyList, jsonOrient,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"datos"}`,
		pageJSONInd(1, 3, false, "Página 1 de 1"),
		`{"fase":"verificar","action":"finish","estado":"terminada","datos":["FV-00001"],"razon":"leido"}`)
	a, _ := newTestAgent(t, chat, gray(), 10)
	out := a.Run(context.Background(), "Lista las facturas vencidas de Ferretería Ruiz")
	if !out.Done || strings.Contains(out.Summary, "Conteo en código") {
		t.Fatalf("dato puntual: done=%v %q", out.Done, out.Summary)
	}

	for in, want := range map[string]bool{
		"¿Cuántas facturas vencidas hay?":          true,
		"cuantos clientes hay en Bogotá":           true,
		"Dame el total facturado en marzo":         true,
		"Suma los valores pendientes":              true,
		"¿Qué cantidad de pedidos quedó abierta?":  true,
		"¿Cuál es el teléfono de Ferretería Ruiz?": false,
		"Lista las facturas vencidas":              false,
	} {
		if got := AsksQuantity(in, nil); got != want {
			t.Errorf("AsksQuantity(%q) = %v", in, got)
		}
	}
}

// Punto 2 (iteracion 3): sin numero de pagina, la cadena debe empezar en la
// primera pagina (anterior deshabilitado o ausente) y, tras un reinicio,
// volver a la misma primera pagina.
func TestE6_5_S3_UnnumberedChainMustStartAtFirstPage(t *testing.T) {
	// La primera extraccion cae en la ultima pagina: no es completa.
	g := NewAggregator("facturas", "Factura", "conteo", nil)
	if _, stop := g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "habilitado", ""))); !stop || g.Complete {
		t.Fatalf("primera extraccion en la ultima pagina: complete=%v %q", g.Complete, g.Stop)
	}
	// El lector no informa "anterior": tampoco consta que sea la primera.
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	if g.AddPage(mustPage(t, pageJSONPrev(1, 7, false, "", ""))); g.Complete {
		t.Fatalf("sin estado de anterior: complete=%v %q", g.Complete, g.Stop)
	}
	// "anterior" ausente en una lista de una pagina: completa.
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	if g.AddPage(mustPage(t, pageJSONPrev(1, 7, false, "ausente", ""))); !g.Complete {
		t.Fatalf("anterior ausente: complete=%v %q", g.Complete, g.Stop)
	}

	// Salto detectado y luego re-extraer la ultima pagina: incompleta.
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONPrev(1, 20, true, "deshabilitado", "")))
	g.NoteAdvance()
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "habilitado", "")))
	if g.Complete || !strings.Contains(g.Stop, "2 avances") {
		t.Fatalf("salto: complete=%v %q", g.Complete, g.Stop)
	}
	g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "habilitado", "")))
	if g.Complete {
		t.Fatalf("re-extraer la ultima pagina tras el reinicio completo la lista: %q", g.Stop)
	}
	// Aun si el lector dijera "anterior deshabilitado", las filas no son las
	// de la primera pagina: "no volviste a la primera pagina".
	g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "deshabilitado", "")))
	if g.Complete || !strings.Contains(g.Stop, "no volviste a la primera pagina") {
		t.Fatalf("reinicio fuera de la primera pagina: complete=%v %q", g.Complete, g.Stop)
	}
	// Una pasada limpia desde la primera pagina completa.
	g.AddPage(mustPage(t, pageJSONPrev(1, 20, true, "deshabilitado", "")))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(21, 20, true, "habilitado", "")))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "habilitado", "")))
	if !g.Complete || g.Count() != 47 {
		t.Fatalf("pasada limpia: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}

	// En el loop: primera extraccion en la ultima pagina, finish rechazado.
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"verificar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSONPrev(41, 7, false, "habilitado", ""),
		`{"fase":"verificar","action":"finish","estado":"terminada","agregado":"conteo","razon":"listo"}`,
		`{"action":"finish","estado":"fallida","razon":"no pude paginar"}`)
	a, _ := newTestAgent(t, chat, gray(), 10)
	out := a.Run(context.Background(), "¿Cuántas facturas vencidas tiene Ferretería Ruiz?")
	if h := strings.Join(out.State.History, "\n"); out.Done || strings.Contains(out.Summary, "respuesta:") || !strings.Contains(h, "no aceptado: la extraccion no esta completa: no consta que esta sea la primera pagina") {
		t.Fatalf("aceptó un conteo desde la ultima pagina: %q\n%s", out.Summary, h)
	}
}

// Punto 3 (iteracion 3): un numero de pagina deducido de un avance no marca
// paginas vistas ni fija el total; la ultima pagina necesita su numero leido.
func TestE6_5_S3_DeducedPageNumberNeverCompletes(t *testing.T) {
	g := NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONPrev(1, 20, true, "deshabilitado", "Página 1")))
	g.NoteAdvance()
	if _, stop := g.AddPage(mustPage(t, pageJSONPrev(21, 5, false, "habilitado", ""))); !stop || g.Complete || !strings.Contains(g.Stop, "no se leyo el numero") {
		t.Fatalf("ultima pagina sin numero leido: complete=%v %q", g.Complete, g.Stop)
	}
	g.AddPage(mustPage(t, pageJSONPrev(21, 5, false, "habilitado", "Página 2")))
	if !g.Complete || g.Count() != 25 {
		t.Fatalf("con el numero leido: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}
	// Un avance que en realidad salto a la 3: la pagina sin numero no cuenta
	// como la 2, y leer luego "Página 3 de 3" sigue pidiendo la 2.
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONPrev(1, 20, true, "deshabilitado", "Página 1 de 3")))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "habilitado", "")))
	g.AddPage(mustPage(t, pageJSONPrev(41, 7, false, "habilitado", "Página 3 de 3")))
	if g.Complete || !strings.Contains(g.Stop, "faltan las paginas 2 de 3") {
		t.Fatalf("pagina deducida marcada como vista: complete=%v %q", g.Complete, g.Stop)
	}
}

// Punto 4 (iteracion 3): un rango de registros no es un numero de pagina.
func TestE6_5_S3_RecordRangeIsNotAPageNumber(t *testing.T) {
	type want struct {
		n, m int
		ok   bool
	}
	for in, w := range map[string]want{
		"Mostrando 1-5 de 5":                   {0, 0, false},
		"Registros 1 a 20 de 45":               {0, 0, false},
		"1–20 de 45":                           {0, 0, false},
		"Showing 21 to 40 of 45":               {0, 0, false},
		"Página 2 de 3":                        {2, 3, true},
		"Page 2 of 5":                          {2, 5, true},
		"Mostrando 1-20 de 45 · Página 1 de 3": {1, 3, true},
		"2 de 3":                               {2, 3, true},
		"Pág. 4":                               {4, 0, true},
	} {
		if n, m, ok := ParseIndicator(in); n != w.n || m != w.m || ok != w.ok {
			t.Errorf("ParseIndicator(%q) = %d %d %v, se esperaba %v", in, n, m, ok, w)
		}
	}
	// Lista de una sola pagina con rango: completa.
	g := NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONPrev(1, 5, false, "deshabilitado", "Mostrando 1-5 de 5")))
	if !g.Complete || g.Count() != 5 {
		t.Fatalf("una pagina con rango: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}
	// Varias paginas con rango: regla sin numero (un avance por pagina).
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONPrev(1, 20, true, "deshabilitado", "Registros 1 a 20 de 45")))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(21, 20, true, "habilitado", "Registros 21 a 40 de 45")))
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(41, 5, false, "habilitado", "Registros 41 a 45 de 45")))
	if !g.Complete || g.Count() != 45 {
		t.Fatalf("rango en varias paginas: complete=%v conteo=%d %q", g.Complete, g.Count(), g.Stop)
	}
	// Un salto con rango se detecta como en cualquier lista sin numero.
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSONPrev(1, 20, true, "deshabilitado", "Registros 1 a 20 de 45")))
	g.NoteAdvance()
	g.NoteAdvance()
	g.AddPage(mustPage(t, pageJSONPrev(41, 5, false, "habilitado", "Registros 41 a 45 de 45")))
	if g.Complete {
		t.Fatalf("salto con rango: %q", g.Stop)
	}
}
