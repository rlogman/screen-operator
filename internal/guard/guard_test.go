package guard

import (
	"context"
	"strings"
	"testing"

	"screenoperator/internal/agent"
)

// ---------------------------------------------------------------- 7.1

func TestE7_1_S1_BulkDeleteRejectedWithoutEvents(t *testing.T) {
	// El modelo no la marca "rechazar": la palabra de policy.destructive basta.
	chat := &routerLLM{script: []string{
		`{"intencion":"editar","entidad":"cliente","objetivo":"clientes inactivos","valores":{},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"eliminar"}`,
	}}
	d := gray()
	a, _ := newAgent(t, chat, d)
	out := a.Run(context.Background(), "Elimina todos los clientes inactivos")
	if out.Done {
		t.Fatalf("terminada: %q", out.Summary)
	}
	if !strings.Contains(out.Summary, "masivo") {
		t.Fatalf("la razon no menciona el cambio masivo: %q", out.Summary)
	}
	if n := inputEvents(d); n != 0 {
		t.Fatalf("el driver recibio %d eventos", n)
	}
	if len(chat.calls) != 1 {
		t.Fatalf("llamadas al modelo = %d (solo clasificar)", len(chat.calls))
	}
}

func TestE7_1_S2_SecretRequestRejectedAndNotLeaked(t *testing.T) {
	chat := &routerLLM{script: []string{
		`{"intencion":"consultar","entidad":"integracion","objetivo":"sistema contable","valores":{},"pregunta":true,"datos_pedidos":["clave de integracion"],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"leer la clave"}`,
	}}
	d := gray()
	a, g := newAgent(t, chat, d)
	g.Redactor().Add("SGC-7781-QQZX") // aunque se hubiera visto, no sale
	out := a.Run(context.Background(), "Dime la clave de integración del sistema contable")
	if out.Done || !strings.Contains(out.Summary, "secreto") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	if strings.Contains(out.Summary, "SGC-7781-QQZX") || strings.Contains(out.Summary, "respuesta:") {
		t.Fatalf("el resumen trae la clave o una respuesta: %q", out.Summary)
	}
	if inputEvents(d) != 0 {
		t.Fatal("el driver recibio eventos")
	}
}

func TestE7_1_S3_CreateWithoutDataNamesMissing(t *testing.T) {
	chat := &routerLLM{script: []string{
		`{"intencion":"crear","entidad":"cliente","objetivo":"","valores":{},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"crear"}`,
	}}
	d := gray()
	a, _ := newAgent(t, chat, d)
	out := a.Run(context.Background(), "Registra un cliente nuevo")
	if out.Done || !strings.Contains(out.Summary, "nombre") || !strings.Contains(out.Summary, "identificador") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	if inputEvents(d) != 0 {
		t.Fatal("el driver recibio eventos")
	}
	// Con faltantes del modelo, se nombran esos.
	g := New().(*Guard)
	s := st("Registra un cliente nuevo", agent.IntentCreate, "", nil)
	s.Class.Missing = []string{"nombre", "NIT"}
	if d := g.Classified(s); !d.Deny || !strings.Contains(d.Reason, "nombre, NIT") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestE7_1_S4_SingleEditIsNotRejected(t *testing.T) {
	g := New().(*Guard)
	s := st("Cambia el teléfono de CL-0007 a 3014445566", agent.IntentEdit, "CL-0007", map[string]string{"telefono": "3014445566"})
	if d := g.Classified(s); d.Deny {
		t.Fatalf("rechazada: %s", d.Reason)
	}
	// Un valor que contiene una palabra de la lista tampoco es masivo.
	s = st("Cambia el estado del pedido PD-0012 a Cancelado", agent.IntentEdit, "PD-0012", map[string]string{"estado": "Cancelado"})
	if d := g.Classified(s); d.Deny {
		t.Fatalf("rechazada: %s", d.Reason)
	}
	// Una consulta con "todas" no se rechaza.
	s = st("¿Cuántas facturas tiene en total, contando todas?", agent.IntentQuery, "", nil)
	if d := g.Classified(s); d.Deny {
		t.Fatalf("consulta rechazada: %s", d.Reason)
	}
}

// ---------------------------------------------------------------- 7.2

var clientCols = []string{"Código", "Nombre", "Documento", "Ciudad"}

func TestE7_2_S1_TwoCandidatesFailNamingBoth(t *testing.T) {
	chat := &routerLLM{script: []string{
		`{"intencion":"editar","entidad":"cliente","objetivo":"Ana Torres","valores":{"telefono":"3014445566"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"buscar y editar"}`,
		orient,
		`{"fase":"localizar","action":"extract","what":"resultados de la busqueda","key":"Código","purpose":"candidatos"}`,
		`{"columnas":["Código","Nombre","Documento","Ciudad"],"filas":[["CL-0005","Ana Torres","52345678","Bogotá"],["CL-0031","Ana Torres","1019876543","Medellín"]],"siguiente":{"visible":false,"habilitado":false},"pagina":null}`,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente","target":"CL-0005"}`,
	}}
	d := gray()
	a, _ := newAgent(t, chat, d)
	out := a.Run(context.Background(), "Cambia el teléfono de Ana Torres a 3014445566.")
	if out.Done {
		t.Fatalf("terminada: %q", out.Summary)
	}
	for _, id := range []string{"52345678", "1019876543"} {
		if !strings.Contains(out.Summary, id) {
			t.Fatalf("el resumen no nombra %s: %q", id, out.Summary)
		}
	}
	if clicksAt(d, 600, 700) != 0 || len(out.State.Commits) != 0 {
		t.Fatalf("se envio el commit: %+v", out.State.Commits)
	}
}

func TestE7_2_S2_CityDisambiguatesSingleCandidate(t *testing.T) {
	instr := "Cambia el teléfono de Abarrotes Moreno de Cali a 3001112233."
	rows := [][]string{{"CL-0011", "Abarrotes Moreno", "900111222", "Pereira"}, {"CL-0012", "Abarrotes Moreno", "900333444", "Cali"}}
	// Con el filtro que respalda la orden.
	g := New().(*Guard)
	s := st(instr, agent.IntentEdit, "Abarrotes Moreno", map[string]string{"telefono": "3001112233"})
	g.ObservePage(s, extract("candidatos", "Código", map[string]string{"Ciudad": "Cali"}), page(clientCols, rows...))
	if c, ok := g.candidates(s); !ok || len(c) != 1 || c[0]["Código"] != "CL-0012" {
		t.Fatalf("candidatos = %v %v", c, ok)
	}
	if d := g.BeforeAction(s, commit("CL-0012", "Guardar cliente")); d.Deny {
		t.Fatalf("bloqueado: %s", d.Reason)
	}
	// Sin filtro: la ciudad de la orden desambigua igual.
	g = New().(*Guard)
	g.ObservePage(s, extract("candidatos", "Código", nil), page(clientCols, rows...))
	if d := g.BeforeAction(s, commit("CL-0012", "Guardar cliente")); d.Deny {
		t.Fatalf("bloqueado sin filtro: %s", d.Reason)
	}
	// Un filtro que la orden no respalda no desambigua.
	g = New().(*Guard)
	s2 := st("Cambia el teléfono de Abarrotes Moreno a 3001112233.", agent.IntentEdit, "Abarrotes Moreno", map[string]string{"telefono": "3001112233"})
	g.ObservePage(s2, extract("candidatos", "Código", map[string]string{"Ciudad": "Pereira"}), page(clientCols, rows...))
	if d := g.BeforeAction(s2, commit("CL-0011", "Guardar cliente")); !d.Deny || !d.Fail {
		t.Fatalf("un filtro inventado paso: %+v", d)
	}
}

func TestE7_2_S3_ZeroCandidatesFailNotFound(t *testing.T) {
	g := New().(*Guard)
	s := st("Cambia el teléfono de Distribuciones Inexistente a 3001112233.", agent.IntentEdit, "Distribuciones Inexistente", map[string]string{"telefono": "3001112233"})
	g.ObservePage(s, extract("candidatos", "Código", nil), page(clientCols))
	d := g.BeforeAction(s, commit("Distribuciones Inexistente", "Guardar cliente"))
	if !d.Deny || !d.Fail || !strings.Contains(d.Reason, "no existe") || !strings.Contains(d.Reason, "Distribuciones Inexistente") {
		t.Fatalf("decision = %+v", d)
	}
	// Si el modelo cierra "no existe" sin el nombre, el guardrail lo agrega.
	f := &agent.FinishRequest{Status: agent.StatusFailed, Reason: "el cliente no existe"}
	if h := g.ReviewFinish(s, f); h != "" {
		t.Fatalf("pidio reintento sin tildes en juego: %s", h)
	}
	d = g.BeforeFinish(s, f)
	if !strings.Contains(d.Reason, "no existe") || !strings.Contains(d.Reason, "Distribuciones Inexistente") {
		t.Fatalf("razon = %q", d.Reason)
	}
}

func TestE7_2_S4_RetryWithoutAccentBeforeNotFound(t *testing.T) {
	g := New().(*Guard)
	s := st("Cambia el teléfono de Ramírez a 3001112233.", agent.IntentEdit, "Ramírez", map[string]string{"telefono": "3001112233"})
	s.Phase = agent.PhaseLocate
	g.BeforeAction(s, agent.Action{Kind: agent.KindType, Phase: agent.PhaseLocate, Text: "Ramírez", Replace: true})
	f := &agent.FinishRequest{Status: agent.StatusFailed, Reason: "no se encontró el cliente"}
	h := g.ReviewFinish(s, f)
	if !strings.Contains(h, `"Ramirez"`) || !strings.Contains(h, `"Ram"`) {
		t.Fatalf("no pidio reintentar sin tilde y con fragmento: %q", h)
	}
	// Tambien al intentar escribir con cero candidatos.
	g.ObservePage(s, extract("candidatos", "Código", nil), page(clientCols))
	if d := g.BeforeAction(s, commit("Ramírez", "Guardar")); !d.Deny || d.Fail || !strings.Contains(d.Reason, "Ramirez") {
		t.Fatalf("decision = %+v", d)
	}
	g.BeforeAction(s, agent.Action{Kind: agent.KindType, Phase: agent.PhaseLocate, Text: "Ramirez", Replace: true})
	g.BeforeAction(s, agent.Action{Kind: agent.KindType, Phase: agent.PhaseLocate, Text: "Ram", Replace: true})
	if h := g.searchVariantsHint("Ramírez"); h != "" {
		t.Fatalf("despues de los reintentos sigue pidiendo: %q", h)
	}
}

// ---------------------------------------------------------------- 7.3

func TestE7_3_S1_CreateExistingNITFailsNamingCode(t *testing.T) {
	chat := &routerLLM{script: []string{
		`{"intencion":"crear","entidad":"cliente","objetivo":"Comercial Andina","valores":{"nombre":"Comercial Andina","nit":"901556874"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"buscar el NIT y crear"}`,
		orient,
		`{"fase":"localizar","action":"type","x":300,"y":130,"text":"901556874","replace":true}`,
		`{"fase":"localizar","action":"extract","what":"resultados por NIT","key":"Código","purpose":"candidatos"}`,
		`{"columnas":["Código","Nombre","NIT"],"filas":[["CL-0042","Comercial Andina S.A.S.","901556874"]],"siguiente":{"visible":false,"habilitado":false},"pagina":null}`,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente nuevo","target":"901556874"}`,
	}}
	d := gray()
	a, _ := newAgent(t, chat, d)
	out := a.Run(context.Background(), "Registra al cliente Comercial Andina, NIT 901556874.")
	if out.Done || !strings.Contains(out.Summary, "CL-0042") {
		t.Fatalf("resumen = %q", out.Summary)
	}
	if clicksAt(d, 600, 700) != 0 {
		t.Fatal("se envio el commit de creacion")
	}
}

func TestE7_3_S2_SecondCommitWithoutVerifyBlocked(t *testing.T) {
	chat := &routerLLM{script: []string{
		`{"intencion":"editar","entidad":"cliente","objetivo":"CL-0007","valores":{"direccion":"Calle 80 # 24-15"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"editar"}`,
		orient,
		`{"fase":"actuar","action":"commit","x":600,"y":700,"what":"Guardar cliente","target":"CL-0007"}`,
		`{"fase":"actuar","action":"commit","x":610,"y":705,"what":"Guardar cliente","target":"CL-0007"}`,
		`{"action":"finish","estado":"fallida","razon":"no se pudo guardar"}`,
	}}
	d := gray()
	a, _ := newAgent(t, chat, d)
	out := a.Run(context.Background(), "Corrige la dirección de CL-0007: Calle 80 # 24-15.")
	if clicksAt(d, 600, 700) != 1 || clicksAt(d, 610, 705) != 0 {
		t.Fatalf("clics: primero %d, segundo %d", clicksAt(d, 600, 700), clicksAt(d, 610, 705))
	}
	if !strings.Contains(strings.Join(out.State.History, "\n"), "BLOQUEADO por guardrail") {
		t.Fatalf("historial sin bloqueo: %v", out.State.History)
	}
}

func TestE7_3_S3_FailedVerifyAllowsOneMoreCommit(t *testing.T) {
	g := New().(*Guard)
	s := st("Corrige la dirección de CL-0007: Calle 80 # 24-15.", agent.IntentEdit, "CL-0007", map[string]string{"direccion": "Calle 80 # 24-15"})
	if d := g.BeforeAction(s, commit("CL-0007", "Guardar cliente")); d.Deny {
		t.Fatal(d.Reason)
	}
	// Relectura: el valor no cambio.
	s.Phase = agent.PhaseVerify
	g.ObservePage(s, extract("datos", "Campo", nil), page([]string{"Campo", "Valor"}, []string{"Dirección", "Cra 1 # 2-3"}))
	if d := g.BeforeAction(s, commit("CL-0007", "Guardar cliente")); d.Deny {
		t.Fatalf("no permitio el reintento: %s", d.Reason)
	}
	if d := g.BeforeAction(s, commit("CL-0007", "Guardar cliente")); !d.Deny {
		t.Fatal("permitio un tercer commit sin verificar")
	}
	g.ObservePage(s, extract("datos", "Campo", nil), page([]string{"Campo", "Valor"}, []string{"Dirección", "Cra 1 # 2-3"}))
	if d := g.BeforeAction(s, commit("CL-0007", "Guardar cliente")); !d.Deny {
		t.Fatal("permitio mas de un reintento")
	}
}

func TestE7_3_S4_ClickOnCommitLabelIsCommit(t *testing.T) {
	s := st("Corrige la dirección de CL-0007: Calle 80 # 24-15.", agent.IntentEdit, "CL-0007", map[string]string{"direccion": "Calle 80 # 24-15"})
	click := func(label string) agent.Action {
		return agent.Action{Kind: agent.KindClick, Phase: agent.PhaseAct, X: 600, Y: 700, Label: label}
	}
	for _, label := range []string{"Guardar", "Exportar"} {
		g := New().(*Guard)
		if !g.WantLabel(s, click("")) {
			t.Fatal("no pide la etiqueta de un click en actuar")
		}
		d := g.BeforeAction(s, click(label))
		if d.Deny || !d.Commit || d.Target != "CL-0007" {
			t.Fatalf("%s: decision = %+v", label, d)
		}
		// Mismas reglas: un segundo commit igual sin verificar se bloquea.
		if d := g.BeforeAction(s, commit("CL-0007", label+" cliente")); !d.Deny {
			t.Fatalf("%s: segundo commit sin verificar paso", label)
		}
	}
	// Eliminar: la orden no lo pide.
	g := New().(*Guard)
	if d := g.BeforeAction(s, click("Eliminar")); !d.Deny {
		t.Fatal("un click en Eliminar paso")
	}
	// Un click sobre un texto comun no es commit.
	if d := g.BeforeAction(s, click("Clientes")); d.Deny || d.Commit {
		t.Fatalf("decision = %+v", d)
	}

	// Extremo a extremo: el loop lee la etiqueta y registra el commit.
	chat := &routerLLM{script: []string{
		`{"intencion":"editar","entidad":"cliente","objetivo":"CL-0007","valores":{"direccion":"Calle 80 # 24-15"},"pregunta":false,"datos_pedidos":[],"fechas":[],"faltantes":[],"razon_rechazo":"","plan":"editar"}`,
		orient,
		`{"fase":"actuar","action":"click","x":600,"y":700}`,
		`{"fase":"actuar","action":"click","x":600,"y":700}`,
		`{"action":"finish","estado":"fallida","razon":"prueba"}`,
	}, label: []string{`{"texto":"Guardar"}`, `{"texto":"Guardar"}`}}
	d := gray()
	a, _ := newAgent(t, chat, d)
	out := a.Run(context.Background(), "Corrige la dirección de CL-0007: Calle 80 # 24-15.")
	if len(out.State.Commits) != 1 || clicksAt(d, 600, 700) != 1 {
		t.Fatalf("commits %d, clics %d", len(out.State.Commits), clicksAt(d, 600, 700))
	}
}
