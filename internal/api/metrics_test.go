package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"screenoperator/internal/tasks"
)

// Costo y pasos por tarea para el reporte del harness: endpoint de diagnostico
// autenticado; GET /tasks/{id} sigue con exactamente estado y resumen.
func TestE5_3_S1_AgentMetricsEndpoint(t *testing.T) {
	r := tasks.RunnerFunc(func(_ context.Context, job *tasks.Job) tasks.Result {
		job.Usage.Add(tasks.Usage{Steps: 1, TokensIn: 1000, TokensOut: 100, CostUSD: 0.002})
		job.Usage.Add(tasks.Usage{Steps: 1, TokensIn: 500, TokensOut: 50, CostUSD: 0.001})
		return tasks.Result{Status: tasks.StatusDone, Summary: "respuesta: 45"}
	})
	h := newHarness(t, r, tasks.Budget{})
	id := h.create(validBody)
	final := h.waitFinal(id, 5*time.Second)
	if len(final) != 2 {
		t.Fatalf("GET /tasks/{id} debe tener exactamente estado y resumen: %v", final)
	}

	res, b := h.do(http.MethodGet, "/tasks/"+id+"/metrics", "Bearer "+testToken, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d %s", res.StatusCode, b)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["pasos"].(float64) != 2 || m["tokens_entrada"].(float64) != 1500 || m["tokens_salida"].(float64) != 150 {
		t.Fatalf("metrics = %v", m)
	}
	if c := m["costo_usd"].(float64); c < 0.00299 || c > 0.00301 {
		t.Fatalf("costo = %v", c)
	}

	// Mismo Bearer que el contrato.
	if res, _ := h.do(http.MethodGet, "/tasks/"+id+"/metrics", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sin token = %d", res.StatusCode)
	}
	if res, _ := h.do(http.MethodGet, "/tasks/"+id+"/metrics", "Bearer otro", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token equivocado = %d", res.StatusCode)
	}
	if res, _ := h.do(http.MethodGet, "/tasks/nope/metrics", "Bearer "+testToken, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("id desconocido = %d", res.StatusCode)
	}
}
