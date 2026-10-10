package harness

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const e3Case = `{"id":"E3","nivel":"regular","instruccion":"¿Cuántas facturas vencidas tiene Ferretería Ruiz?","estado_esperado":"terminada","respuesta_esperada":"45","diff_esperado":[],"lista_blanca":[]}`

func officialCase(t *testing.T, name string) Case {
	t.Helper()
	p := filepath.Join("..", "..", "material", "pruebas", name)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCase(p, raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestE5_1_S1_RunsCaseResetPostPollCheckJSONL(t *testing.T) {
	w := newWorld(t)
	w.finishAt = 6 * time.Second
	w.resumen = "Busqué las facturas.\nrespuesta: 45"
	var out bytes.Buffer
	r := w.runner(&out)
	c := officialCase(t, "E3-facturas-vencidas.json")
	recs, err := r.Run(context.Background(), []Case{c})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("registros = %d", len(recs))
	}

	// Orden y tiempos de las llamadas.
	ev := w.events()
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.What)
	}
	want := []string{"reset", "post", "get", "get", "check"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("llamadas = %v, se esperaba %v", kinds, want)
	}
	// GET cada 5 s desde el POST: a los 5 s (en_curso) y a los 10 s (terminada).
	if ev[2].At-ev[1].At != 5*time.Second || ev[3].At-ev[2].At != 5*time.Second {
		t.Fatalf("intervalos de GET: %v y %v", ev[2].At-ev[1].At, ev[3].At-ev[2].At)
	}
	// /__check recibe el JSON del caso tal cual.
	if ev[4].Body != string(c.Raw) {
		t.Fatalf("/__check recibio otro cuerpo:\n%s", ev[4].Body)
	}
	// El agente recibe el websocket y la contraseña.
	if !strings.HasSuffix(ev[1].Body, "|wss://desk.test/websockify") || w.lastPass != "vnc-pass-123" {
		t.Fatalf("POST /tasks: %q", ev[1].Body)
	}

	lines := decodeJSONL(t, out.String())
	if len(lines) != 1 {
		t.Fatalf("lineas JSONL = %d", len(lines))
	}
	l := lines[0]
	if l["id"] != "E3" || l["estado"] != "terminada" || l["resumen"] != w.resumen {
		t.Fatalf("linea = %v", l)
	}
	if l["duracion_s"].(float64) != 10 {
		t.Fatalf("duracion_s = %v", l["duracion_s"])
	}
	chk, ok := l["check"].(map[string]any)
	if !ok || chk["pass"] != true || len(chk["checks"].([]any)) != 3 {
		t.Fatalf("check = %v", l["check"])
	}
	if recs[0].Puntaje == nil || !recs[0].Puntaje.Correcta {
		t.Fatalf("puntaje = %+v", recs[0].Puntaje)
	}
	// Ningun secreto en el JSONL.
	for _, s := range []string{"vnc-pass-123", "team-tok", "desk-tok"} {
		if strings.Contains(out.String(), s) {
			t.Fatalf("el JSONL contiene un secreto: %s", out.String())
		}
	}
}

func TestE5_1_S2_TimeoutStopsPollingAndMovesOn(t *testing.T) {
	// Reloj falso: 300 s y 5 s reales del harness, sin esperar.
	w := newWorld(t)
	w.finishAt = -1 // nunca termina
	var out bytes.Buffer
	r := w.runner(&out)
	c1 := mustCase(t, `{"id":"A","instruccion":"x","estado_esperado":"terminada"}`)
	c2 := mustCase(t, `{"id":"B","instruccion":"y","estado_esperado":"terminada"}`)
	recs, err := r.Run(context.Background(), []Case{c1, c2})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Estado != EstadoTimeout || recs[1].Estado != EstadoTimeout {
		t.Fatalf("registros = %+v", recs)
	}
	if recs[0].DuracionS != 300 {
		t.Fatalf("duracion = %v", recs[0].DuracionS)
	}
	gets := 0
	var lastGet, nextReset time.Duration
	for _, e := range w.events() {
		if e.What == "get" && e.Body == "t1" {
			gets++
			lastGet = e.At
		}
		if e.What == "reset" && e.At > 0 && nextReset == 0 {
			nextReset = e.At
		}
	}
	if gets != 60 {
		t.Fatalf("GET del primer caso = %d, se esperaban 60 (cada 5 s hasta 300 s)", gets)
	}
	if nextReset < lastGet || nextReset == 0 {
		t.Fatalf("el segundo caso no empezo despues del ultimo GET: %v / %v", nextReset, lastGet)
	}
	lines := decodeJSONL(t, out.String())
	if len(lines) != 2 || lines[0]["estado"] != "timeout" {
		t.Fatalf("JSONL = %v", lines)
	}

	// Escalado en tiempo real: 10 ms entre consultas y 60 ms de limite.
	w2 := newWorld(t)
	w2.finishAt = -1
	r2 := w2.runner(nil)
	r2.Now, r2.Sleep = time.Now, nil
	r2.PollInterval, r2.Timeout = 10*time.Millisecond, 60*time.Millisecond
	start := time.Now()
	recs2, err := r2.Run(context.Background(), []Case{c1})
	if err != nil || len(recs2) != 1 || recs2[0].Estado != EstadoTimeout {
		t.Fatalf("escalado: %v %+v", err, recs2)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("el timeout escalado tardo %v", el)
	}
}

func TestE5_1_S3_RunsOptionRepeatsWithReset(t *testing.T) {
	w := newWorld(t)
	var out bytes.Buffer
	r := w.runner(&out)
	r.Runs = 3
	recs, err := r.Run(context.Background(), []Case{mustCase(t, e3Case)})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("registros = %d", len(recs))
	}
	var seq []string
	for _, e := range w.events() {
		if e.What == "reset" || e.What == "post" {
			seq = append(seq, e.What)
		}
	}
	if strings.Join(seq, ",") != "reset,post,reset,post,reset,post" {
		t.Fatalf("secuencia = %v", seq)
	}
	lines := decodeJSONL(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("lineas = %d", len(lines))
	}
	for i, l := range lines {
		if int(l["corrida"].(float64)) != i+1 {
			t.Fatalf("corrida %d = %v", i, l["corrida"])
		}
	}
}

func TestE5_1_S4_CasesGlobListAlphabetical(t *testing.T) {
	root := t.TempDir()
	body := func(id string) string {
		return `{"id":"` + id + `","instruccion":"x","estado_esperado":"terminada"}`
	}
	writeCase(t, root, "material/pruebas/E1-crear.json", body("E1"))
	writeCase(t, root, "material/pruebas/E10-otro.json", body("E10"))
	writeCase(t, root, "material/pruebas/E2-dir.json", body("E2"))
	writeCase(t, root, "cases/b-editar.json", body("b"))
	writeCase(t, root, "cases/a-crear.json", body("a"))
	writeCase(t, root, "cases/notas.txt", "no es un caso")

	wd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	cases, warn, err := LoadCases("material/pruebas/E1*.json,cases/*.json")
	if err != nil || len(warn) != 0 {
		t.Fatalf("LoadCases: %v %v", err, warn)
	}
	var got []string
	for _, c := range cases {
		got = append(got, c.Path)
	}
	want := []string{"cases/a-crear.json", "cases/b-editar.json", "material/pruebas/E1-crear.json", "material/pruebas/E10-otro.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("casos = %v, se esperaba %v", got, want)
	}

	// Se corren todos, en ese orden.
	w := newWorld(t)
	r := w.runner(nil)
	recs, err := r.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, rc := range recs {
		ids = append(ids, rc.ID)
	}
	if strings.Join(ids, ",") != "a,b,E1,E10" {
		t.Fatalf("orden de corrida = %v", ids)
	}

	// Un patron sin coincidencias avisa; ningun caso en total es error.
	_, warn, err = LoadCases("cases/*.json,nada/*.json")
	if err != nil || len(warn) != 1 {
		t.Fatalf("patron vacio: %v %v", err, warn)
	}
	if _, _, err := LoadCases("nada/*.json"); err == nil {
		t.Fatal("sin casos deberia ser error")
	}
}
