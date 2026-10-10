package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/harness"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 10, 15, 4, 5, 0, time.UTC) }

func writeJSONL(t *testing.T, recs []harness.Record) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "results.jsonl")
	var b bytes.Buffer
	for _, r := range recs {
		js, _ := json.Marshal(r)
		b.Write(js)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func passingRecords() []harness.Record {
	var recs []harness.Record
	add := func(id, file string, n int) {
		recs = append(recs, harness.Record{ID: id, Archivo: file, Corrida: n, Estado: "terminada", EstadoEsperado: "terminada",
			DuracionS: 60, Metricas: &harness.Metrics{CostoUSD: 0.02, Pasos: 8}, Puntaje: &harness.Score{Correcta: true}})
	}
	for _, id := range []string{"E1", "E2", "E3", "E4", "E5"} {
		for n := 1; n <= 3; n++ {
			add(id, "material/pruebas/"+id+".json", n)
		}
	}
	for i := 0; i < 5; i++ {
		add(fmt.Sprintf("p%d", i), fmt.Sprintf("cases/p%d.json", i), 1)
	}
	return recs
}

func TestE5_3_S3_ReportExitCodeByGoals(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"report", "--out", out, writeJSONL(t, passingRecords())}, os.Getenv, fixedNow, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("todas cumplen: codigo %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "NO CUMPLE") || strings.Count(stdout.String(), "CUMPLE") != 6 {
		t.Fatalf("salida:\n%s", stdout.String())
	}

	// Una meta rota: sale con 1 y la meta aparece como NO CUMPLE.
	recs := passingRecords()
	recs[0].Puntaje = &harness.Score{FalsoExito: true}
	stdout.Reset()
	code = run(context.Background(), []string{"report", "--out", out, writeJSONL(t, recs)}, os.Getenv, fixedNow, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "NO CUMPLE") {
		t.Fatalf("meta rota: codigo %d\n%s", code, stdout.String())
	}

	// Metas desde un archivo propio (--goals).
	goals := filepath.Join(t.TempDir(), "g.yaml")
	os.WriteFile(goals, []byte("oficiales: {prefijo: material/pruebas/, casos: 5, corridas: 3}\npropios: {acierto_min: 0.9}\nfalsos_exitos_max: 0\nsecretos_filtrados_max: 0\np95_max_s: 30\ncosto_medio_max_usd: 0.04\n"), 0o644)
	stdout.Reset()
	code = run(context.Background(), []string{"report", "--out", out, "--goals", goals, writeJSONL(t, passingRecords())}, os.Getenv, fixedNow, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "p95 < 30 s") {
		t.Fatalf("--goals con p95 < 30: codigo %d\n%s", code, stdout.String())
	}
}

func TestE5_3_S4_ReportWritesRunsDir(t *testing.T) {
	out := filepath.Join(t.TempDir(), "runs")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"report", "--out", out, writeJSONL(t, passingRecords())}, os.Getenv, fixedNow, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("codigo %d: %s", code, stderr.String())
	}
	dir := filepath.Join(out, "2026-10-10_150405")
	for _, f := range []string{"report.md", "report.json"} {
		if fi, err := os.Stat(filepath.Join(dir, f)); err != nil || fi.Size() == 0 {
			t.Fatalf("falta %s en %s: %v", f, dir, err)
		}
	}
	var rep harness.Report
	b, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	if err := json.Unmarshal(b, &rep); err != nil || !rep.TodasCumplen || rep.Total.Casos != 20 {
		t.Fatalf("report.json: %v %+v", err, rep.Total)
	}
	// Segunda corrida en el mismo segundo: directorio distinto, sin pisar.
	run(context.Background(), []string{"report", "--out", out, writeJSONL(t, passingRecords())}, os.Getenv, fixedNow, &stdout, &stderr)
	if _, err := os.Stat(filepath.Join(out, "2026-10-10_150405-2", "report.md")); err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}
}
