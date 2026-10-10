package guard

import (
	"context"
	"strings"
	"testing"

	"screenoperator/internal/agent"
)

func editState() *agent.State {
	return st("Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.", agent.IntentEdit,
		"Distribuciones El Faro", map[string]string{"direccion": "Calle 80 # 24-15"})
}

func finishDone(evidence string) *agent.FinishRequest {
	return &agent.FinishRequest{Status: agent.StatusDone, Reason: "guardado", Verified: true, Evidence: evidence}
}

func TestE7_4_S1_ReadBackMatchesIsDone(t *testing.T) {
	g := New().(*Guard)
	s := editState()
	g.BeforeAction(s, commit("CL-0007", "Guardar cliente"))
	s.Phase = agent.PhaseVerify
	g.ObservePage(s, extract("datos", "Campo", nil), page([]string{"Campo", "Valor"}, []string{"Código", "CL-0007"}, []string{"Dirección", "Calle 80 # 24-15 "}))
	if d := g.BeforeFinish(s, finishDone("")); d.Deny {
		t.Fatalf("rechazada: %s", d.Reason)
	}
	// Extremo a extremo con el loop.
	chat := &routerLLM{script: []string{
		`{"intencion":"editar","entidad":"cliente","objetivo":"Distribuciones El Faro","valores":{"direccion":"Calle 80 # 24-15"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"editar"}`,
		orient,
		`{"fase":"actuar","action":"type","x":400,"y":350,"text":"Calle 80 # 24-15","replace":true}`,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente CL-0007","target":"CL-0007"}`,
		`{"fase":"verificar","action":"extract","what":"ficha del cliente","key":"Campo","purpose":"datos"}`,
		`{"columnas":["Campo","Valor"],"filas":[["Código","CL-0007"],["Dirección","Calle 80 # 24-15"]],"siguiente":{"visible":false,"habilitado":false},"pagina":null}`,
		`{"action":"finish","estado":"terminada","razon":"direccion guardada","entidad":"cliente","id_registro":"CL-0007","cambios":["direccion = Calle 80 # 24-15"],"verificado":true,"evidencia":"Calle 80 # 24-15"}`,
	}}
	a, _ := newAgent(t, chat, gray())
	out := a.Run(context.Background(), s.Instruction)
	if !out.Done {
		t.Fatalf("no termino: %q\n%v", out.Summary, out.State.History)
	}
}

func TestE7_4_S2_ReadBackDiffersIsFailedNamingBoth(t *testing.T) {
	g := New().(*Guard)
	s := editState()
	g.BeforeAction(s, commit("CL-0007", "Guardar cliente"))
	s.Phase = agent.PhaseVerify
	g.ObservePage(s, extract("datos", "Campo", nil), page([]string{"Campo", "Valor"}, []string{"Dirección", "Calle 80 # 24-1"}))
	d := g.BeforeFinish(s, finishDone("Calle 80 # 24-15"))
	if !d.Deny || !strings.Contains(d.Reason, `"Calle 80 # 24-15"`) || !strings.Contains(d.Reason, `"Calle 80 # 24-1"`) {
		t.Fatalf("decision = %+v", d)
	}
	// Solo con la evidencia del modelo: tambien exacta (24-150 no es 24-15).
	g2 := New().(*Guard)
	s2 := editState()
	if d := g2.BeforeFinish(s2, finishDone("la ficha muestra Calle 80 # 24-150")); !d.Deny {
		t.Fatal("aceptó 24-150 como 24-15")
	}
	if d := g2.BeforeFinish(s2, finishDone("la ficha muestra calle 80 # 24-15")); !d.Deny {
		t.Fatal("las mayusculas deben compararse exactas")
	}
}

func TestE7_4_S3_CreateRequiresExactlyOneByID(t *testing.T) {
	s := st("Registra al cliente Papelería Los Andes, NIT 901234567.", agent.IntentCreate, "Papelería Los Andes",
		map[string]string{"nombre": "Papelería Los Andes", "nit": "901234567"})
	cols := []string{"Código", "Nombre", "NIT"}
	g := New().(*Guard)
	g.BeforeAction(s, agent.Action{Kind: agent.KindType, Phase: agent.PhaseLocate, Text: "901234567", Replace: true})
	g.ObservePage(s, extract("candidatos", "Código", nil), page(cols))
	if d := g.BeforeAction(s, commit("901234567", "Guardar cliente")); d.Deny {
		t.Fatalf("commit bloqueado: %s", d.Reason)
	}
	s.Phase = agent.PhaseVerify
	if h := g.ReviewFinish(s, finishDone("creado")); !strings.Contains(h, "identificador") {
		t.Fatalf("sin relectura no pidio buscar por identificador: %q", h)
	}
	g.ObservePage(s, extract("candidatos", "Código", nil), page(cols, []string{"CL-0051", "Papelería Los Andes", "901234567"}))
	if d := g.BeforeFinish(s, finishDone("")); d.Deny {
		t.Fatalf("un registro: %s", d.Reason)
	}
	g.ObservePage(s, extract("candidatos", "Código", nil), page(cols, []string{"CL-0051", "Papelería Los Andes", "901234567"}, []string{"CL-0052", "Papelería Los Andes", "901234567"}))
	d := g.BeforeFinish(s, finishDone(""))
	if !d.Deny || !strings.Contains(d.Reason, "duplicado") || !strings.Contains(d.Reason, "CL-0052") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestE7_4_S4_ExportOneNewReportWithFilters(t *testing.T) {
	s := st("Exporta el reporte de pedidos entregados en septiembre de 2026.", agent.IntentExport, "pedidos",
		map[string]string{"estado": "Entregado", "periodo": "septiembre de 2026"})
	cols := []string{"Código", "Lista", "Filtros", "Fecha"}
	old := []string{"RP-0001", "Facturas", "Estado=Vencida", "13/10/2026"}
	run := func(post ...[]string) agent.Decision {
		g := New().(*Guard)
		g.ObservePage(s, extract("datos", "Código", nil), page(cols, old))
		g.BeforeAction(s, commit("pedidos", "Exportar pedidos"))
		s.Phase = agent.PhaseVerify
		g.ObservePage(s, extract("datos", "Código", nil), page(cols, post...))
		return g.BeforeFinish(s, finishDone(""))
	}
	if d := run(old, []string{"RP-0002", "Pedidos", "Estado=Entregado; 01/09/2026-30/09/2026", "14/10/2026"}); d.Deny {
		t.Fatalf("un reporte nuevo: %s", d.Reason)
	}
	if d := run(old, []string{"RP-0002", "Pedidos", "Estado=Entregado", "14/10/2026"}, []string{"RP-0003", "Pedidos", "Estado=Entregado", "14/10/2026"}); !d.Deny || !strings.Contains(d.Reason, "duplicado") {
		t.Fatalf("dos nuevos: %+v", d)
	}
	if d := run(old, []string{"RP-0002", "Pedidos", "Estado=Pendiente", "14/10/2026"}); !d.Deny {
		t.Fatal("filtros distintos aceptados")
	}
	if d := run(old); !d.Deny {
		t.Fatal("sin reporte nuevo aceptado")
	}
}

func TestE7_4_S5_UnreadableVerificationIsFailed(t *testing.T) {
	g := New().(*Guard)
	s := editState()
	g.BeforeAction(s, commit("CL-0007", "Guardar cliente"))
	s.Phase = agent.PhaseVerify
	d := g.BeforeFinish(s, finishDone(""))
	if !d.Deny || !strings.Contains(d.Reason, "no se pudo leer") {
		t.Fatalf("decision = %+v", d)
	}
	// Extremo a extremo: verificado sin evidencia ni lectura -> fallida.
	chat := &routerLLM{script: []string{
		`{"intencion":"editar","entidad":"cliente","objetivo":"Distribuciones El Faro","valores":{"direccion":"Calle 80 # 24-15"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"editar"}`,
		orient,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente","target":"CL-0007"}`,
		`{"fase":"verificar","action":"click","x":200,"y":300}`,
		`{"action":"finish","estado":"terminada","razon":"ok","verificado":true}`,
	}}
	a, _ := newAgent(t, chat, gray())
	out := a.Run(context.Background(), s.Instruction)
	if out.Done {
		t.Fatalf("terminada sin poder leer: %q", out.Summary)
	}
}

// Modal de confirmacion al guardar: un commit que quedo pendiente de un
// dialogo no se reporta terminada.
func TestConfirmDialogPendingIsNotDone(t *testing.T) {
	script := func(confirm bool) []string {
		s := []string{
			`{"intencion":"editar","entidad":"cliente","objetivo":"Distribuciones El Faro","valores":{"direccion":"Calle 80 # 24-15"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"editar"}`,
			orient,
			`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente","target":"CL-0007"}`,
		}
		if confirm {
			s = append(s, `{"fase":"actuar","action":"commit","x":640,"y":420,"what":"Aceptar confirmacion de guardado","target":"CL-0007","dialogo":true}`)
		}
		return append(s,
			`{"fase":"verificar","action":"click","x":200,"y":300}`,
			`{"action":"finish","estado":"terminada","razon":"ok","verificado":true,"evidencia":"Calle 80 # 24-15"}`,
			`{"action":"finish","estado":"terminada","razon":"ok","verificado":true,"evidencia":"Calle 80 # 24-15"}`)
	}
	pending := `{"dialogo":true,"texto":"¿Confirma guardar los cambios del cliente CL-0007? Aceptar / Cancelar"}`
	chat := &routerLLM{script: script(false), dialog: []string{pending}}
	a, _ := newAgent(t, chat, gray())
	out := a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	if out.Done || !strings.Contains(out.Summary, "diálogo") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	// Confirmado: termina.
	chat = &routerLLM{script: script(true), dialog: []string{pending, `{"dialogo":false,"texto":""}`}}
	a, _ = newAgent(t, chat, gray())
	out = a.Run(context.Background(), "Corrige la dirección del cliente Distribuciones El Faro: ahora es Calle 80 # 24-15.")
	if !out.Done {
		t.Fatalf("confirmado y no termino: %q\n%v", out.Summary, out.State.History)
	}
}

// Falso exito medido en E3 (27 en vez de 45): el indicador de pagina manda.
func TestIncompletePagesBlockAnswer(t *testing.T) {
	g := New().(*Guard)
	s := st("¿Cuántas facturas vencidas tiene Ferretería Ruiz?", agent.IntentQuery, "Ferretería Ruiz", nil)
	s.Phase = agent.PhaseVerify
	cols := []string{"Número", "Estado"}
	g.ObservePage(s, extract("conteo", "Número", nil), agent.Page{Columns: cols, Indicator: "Página 1 de 3", Rows: page(cols, []string{"F-1", "Vencida"}).Rows})
	g.ObservePage(s, extract("conteo", "Número", nil), agent.Page{Columns: cols, Indicator: "Página 2 de 3", Rows: page(cols, []string{"F-2", "Vencida"}).Rows})
	g.ObservePage(s, extract("conteo", "Número", nil), agent.Page{Columns: cols, Indicator: "Página 2 de 3", Rows: page(cols, []string{"F-2", "Vencida"}).Rows})
	f := &agent.FinishRequest{Status: agent.StatusDone, Aggregate: "conteo", Reason: "conteo"}
	if h := g.ReviewFinish(s, f); !strings.Contains(h, "página 3") {
		t.Fatalf("hint = %q", h)
	}
	if d := g.BeforeFinish(s, f); !d.Deny {
		t.Fatal("aceptó un conteo parcial")
	}
	g.ObservePage(s, extract("conteo", "Número", nil), agent.Page{Columns: cols, Indicator: "Página 3 de 3", Rows: page(cols, []string{"F-3", "Vencida"}).Rows})
	if d := g.BeforeFinish(s, f); d.Deny {
		t.Fatalf("rechazó un conteo completo: %s", d.Reason)
	}
	// "Mostrando 1-20 de 45" no es un indicador de paginas.
	g2 := New().(*Guard)
	g2.ObservePage(s, extract("conteo", "Número", nil), agent.Page{Columns: cols, Indicator: "Mostrando 1-20 de 45"})
	if why := g2.incomplete(); why != "" {
		t.Fatalf("incompleto por un total de filas: %s", why)
	}
}
