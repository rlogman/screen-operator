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
func pageJSON(first, n int, next bool) string {
	type row map[string]string
	rows := []row{}
	for i := 0; i < n; i++ {
		rows = append(rows, row{"Factura": fmt.Sprintf("FV-%05d", first+i), "Cliente": "Ferretería Ruiz", "Estado": "Vencida", "Valor": "1.250.000"})
	}
	b, _ := json.Marshal(map[string]any{
		"columnas": []string{"Factura", "Cliente", "Estado", "Valor"}, "filas": rows,
		"siguiente": map[string]bool{"visible": true, "habilitado": next}, "pagina": nil,
	})
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
	g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	g.AddPage(mustPage(t, pageJSON(21, 20, true)))
	_, stop := g.AddPage(mustPage(t, pageJSON(41, 5, false)))
	if !stop {
		t.Fatal("la ultima pagina debia cerrar la extraccion")
	}
	if c := g.Count(); c != 45 {
		t.Fatalf("conteo = %d, se esperaba 45", c)
	}

	// Extremo a extremo: el loop extrae cada pagina y el codigo pone el 45.
	chat := fakes.NewScriptedLLM(jsonClassifyQuery, jsonOrient,
		`{"fase":"localizar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo","filters":{"Estado":"Vencida","Cliente":"Ferretería Ruiz"}}`,
		pageJSON(1, 20, true),
		`{"fase":"localizar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"localizar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSON(21, 20, true),
		`{"fase":"localizar","action":"click","x":1200,"y":760,"reason":"Siguiente"}`,
		`{"fase":"localizar","action":"extract","what":"facturas","key":"Factura","purpose":"conteo"}`,
		pageJSON(41, 5, false),
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
	if _, stop := g.AddPage(mustPage(t, pageJSON(21, 3, false))); !stop || !strings.Contains(g.Stop, "siguiente") {
		t.Fatalf("Siguiente deshabilitado: stop=%v %q", stop, g.Stop)
	}
	g = NewAggregator("facturas", "Factura", "conteo", nil)
	g.AddPage(mustPage(t, pageJSON(1, 20, true)))
	// La app no avanzo (clic sin efecto): la pagina nueva repite la anterior.
	if _, stop := g.AddPage(mustPage(t, pageJSON(1, 20, true))); !stop || !strings.Contains(g.Stop, "repite") {
		t.Fatalf("pagina repetida: stop=%v %q", stop, g.Stop)
	}
	// Sin boton siguiente visible tambien se detiene.
	g = NewAggregator("x", "", "datos", nil)
	if _, stop := g.AddPage(mustPage(t, `{"filas":[{"a":"1"}],"siguiente":{"visible":false,"habilitado":true}}`)); !stop {
		t.Fatal("sin boton siguiente visible debia detenerse")
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
