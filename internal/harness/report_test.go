package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goalsForTest(t *testing.T) Goals {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "harness", "goals.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseGoals(b)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// rec arma un registro ya puntuado.
func rec(id, archivo, cat string, ok bool, dur, cost float64, steps int, mut func(*Record)) Record {
	r := Record{ID: id, Archivo: archivo, Categoria: cat, Corrida: 1, Estado: "terminada", EstadoEsperado: "terminada",
		DuracionS: dur, Metricas: &Metrics{CostoUSD: cost, Pasos: steps}, Puntaje: &Score{Correcta: ok}}
	if mut != nil {
		mut(&r)
	}
	return r
}

func TestE5_3_S1_ReportMetrics(t *testing.T) {
	var recs []Record
	for i := 1; i <= 10; i++ {
		ok := i <= 8
		recs = append(recs, rec(fmt.Sprintf("c%d", i), fmt.Sprintf("cases/c%d.json", i), "consultar", ok, float64(i*10), 0.01*float64(i), i, nil))
	}
	recs[8].Puntaje = &Score{FalsoExito: true, FueraDeTarea: true}
	recs[9].Puntaje = &Score{SecretoFiltrado: true, Duplicados: true}
	rep := BuildReport(recs, goalsForTest(t), []string{"x.jsonl"})
	s := rep.Total
	if s.Casos != 10 || s.Correctos != 8 || s.Acierto != 0.8 {
		t.Fatalf("acierto: %+v", s)
	}
	if s.FalsosExitos != 1 || s.FueraDeTarea != 1 || s.Duplicados != 1 || s.Secretos != 1 {
		t.Fatalf("contadores: %+v", s)
	}
	// Duraciones 10..100: p50 = 50, p95 = 100 (rango mas cercano).
	if s.P50S != 50 || s.P95S != 100 {
		t.Fatalf("p50/p95 = %v/%v", s.P50S, s.P95S)
	}
	if s.CostoMedioUSD == nil || fmt.Sprintf("%.3f", *s.CostoMedioUSD) != "0.055" || *s.PasosMedios != 5.5 {
		t.Fatalf("costo/pasos: %v %v", s.CostoMedioUSD, s.PasosMedios)
	}
	md := rep.Markdown()
	for _, want := range []string{"80.0 %", "Falsos éxitos", "Fuera de la tarea", "Duplicados", "Secretos filtrados", "p50", "p95", "Costo medio", "Pasos medios"} {
		if !strings.Contains(md, want) {
			t.Fatalf("el reporte no muestra %q:\n%s", want, md)
		}
	}
}

func TestE5_3_S2_ReportPerCategory(t *testing.T) {
	recs := []Record{
		rec("a", "cases/a.json", "crear", true, 10, 0.01, 3, nil),
		rec("b", "cases/b.json", "crear", false, 20, 0.02, 5, nil),
		rec("c", "cases/c.json", "rechazar", true, 30, 0.03, 7, nil),
		rec("E1", "material/pruebas/E1-crear-cliente.json", "", true, 40, 0.04, 9, nil),
	}
	rep := BuildReport(recs, goalsForTest(t), nil)
	cr, ok := rep.PorCategoria["crear"]
	if !ok || cr.Casos != 2 || cr.Correctos != 1 || cr.Acierto != 0.5 || cr.P95S != 20 || *cr.PasosMedios != 4 {
		t.Fatalf("crear = %+v", cr)
	}
	if rep.PorCategoria["rechazar"].Casos != 1 || rep.PorCategoria[CategoriaOficial].Casos != 1 {
		t.Fatalf("categorias = %+v", rep.PorCategoria)
	}
	md := rep.Markdown()
	for _, want := range []string{"| crear |", "| rechazar |", "| oficial |"} {
		if !strings.Contains(md, want) {
			t.Fatalf("falta la fila %q:\n%s", want, md)
		}
	}
}

// goalsRecords arma un conjunto que cumple todas las metas por defecto.
func goalsRecords() []Record {
	var recs []Record
	for _, id := range []string{"E1", "E2", "E3", "E4", "E5"} {
		for n := 1; n <= 3; n++ {
			recs = append(recs, rec(id, "material/pruebas/"+id+".json", "", true, 60, 0.02, 10, func(r *Record) { r.Corrida = n }))
		}
	}
	for i := 0; i < 10; i++ {
		recs = append(recs, rec(fmt.Sprintf("p%d", i), fmt.Sprintf("cases/p%d.json", i), "consultar", true, 90, 0.03, 12, nil))
	}
	return recs
}

func TestE5_3_S3_GoalsCumpleNoCumple(t *testing.T) {
	g := goalsForTest(t)
	rep := BuildReport(goalsRecords(), g, nil)
	if !rep.TodasCumplen {
		t.Fatalf("deberian cumplir todas: %+v", rep.Metas)
	}
	if len(rep.Metas) != 6 {
		t.Fatalf("metas = %d, se esperaban 6", len(rep.Metas))
	}
	for _, m := range rep.Metas {
		if !strings.Contains(rep.Markdown(), "| "+m.Meta+" | "+m.Valor+" | CUMPLE |") {
			t.Fatalf("la meta %q no aparece como CUMPLE:\n%s", m.Meta, rep.Markdown())
		}
	}
	// Cada meta, rota por separado, aparece como NO CUMPLE.
	breakers := map[string]func([]Record) []Record{
		"oficiales":          func(r []Record) []Record { r[0].Puntaje = &Score{}; return r },
		"oficiales corridas": func(r []Record) []Record { return r[1:] },
		"propios": func(r []Record) []Record {
			r[15].Puntaje, r[16].Puntaje = &Score{}, &Score{}
			return r
		},
		"falsos": func(r []Record) []Record { r[15].Puntaje = &Score{Correcta: true, FalsoExito: true}; return r },
		"secretos": func(r []Record) []Record {
			r[15].Puntaje = &Score{Correcta: true, SecretoFiltrado: true}
			return r
		},
		"p95": func(r []Record) []Record {
			for i := range r {
				r[i].DuracionS = 240
			}
			return r
		},
		"costo": func(r []Record) []Record { r[0].Metricas.CostoUSD = 2; return r },
	}
	for name, br := range breakers {
		rep := BuildReport(br(goalsRecords()), g, nil)
		if rep.TodasCumplen || !strings.Contains(rep.Markdown(), "NO CUMPLE") {
			t.Fatalf("%s: deberia haber una meta NO CUMPLE: %+v", name, rep.Metas)
		}
	}
	// Las metas vienen del archivo, no de constantes.
	g2 := g
	g2.CostoMedioMaxUSD = 0.01
	if BuildReport(goalsRecords(), g2, nil).TodasCumplen {
		t.Fatal("con costo maximo 0.01 no deberia cumplir")
	}
	if _, err := ParseGoals([]byte("oficiales: {prefijo: x, casos: 5, corridas: 3}\npropios: {acierto_min: 0.9}\np95_max_s: 240\ncosto_medio_max_usd: 0.04\nclave_rara: 1\n")); err == nil {
		t.Fatal("una clave desconocida en metas deberia ser error")
	}
}

func TestE5_3_S4_WritesReportFiles(t *testing.T) {
	dir := t.TempDir()
	rep := BuildReport(goalsRecords(), goalsForTest(t), []string{"a.jsonl"})
	if err := WriteReport(dir, rep); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"report.md", "report.json"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// Ida y vuelta: el runner escribe un JSONL que el reporte vuelve a leer.
	w := newWorld(t)
	w.metrics = Metrics{CostoUSD: 0.02, Pasos: 7}
	var out bytes.Buffer
	if _, err := w.runner(&out).Run(context.Background(), []Case{mustCase(t, e3Case)}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "results.jsonl")
	os.WriteFile(p, out.Bytes(), 0o644)
	recs, err := ReadRecords(p)
	if err != nil || len(recs) != 1 || recs[0].Metricas == nil || recs[0].Metricas.Pasos != 7 {
		t.Fatalf("ReadRecords: %v %+v", err, recs)
	}
}
