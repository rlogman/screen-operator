package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/tasks"
)

// --- 3.4-S5 panic ---

func TestE3_4_S5_PanicFailsTaskProcessSurvives(t *testing.T) {
	const secretPanic = "detalle interno P4ss-xyz"
	h := newHarness(t, tasks.RunnerFunc(func(context.Context, *tasks.Job) tasks.Result {
		panic(secretPanic)
	}), tasks.Budget{})
	id := h.create(validBody)
	m := h.waitFinal(id, 5*time.Second)
	if m["estado"] != "fallida" {
		t.Fatalf("estado = %v, se esperaba fallida", m["estado"])
	}
	if s, _ := m["resumen"].(string); s != tasks.SummaryInternal || strings.Contains(s, "detalle") {
		t.Fatalf("resumen = %q, se esperaba el generico", s)
	}
	if res, _ := h.do(http.MethodGet, "/health", "Bearer "+testToken, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("/health tras el panic = %d", res.StatusCode)
	}
	// El proceso sigue aceptando tareas.
	id2 := h.create(validBody)
	if code, _ := h.status(id2); code != http.StatusOK {
		t.Fatalf("GET tras panic = %d", code)
	}
	if strings.Contains(h.log.String(), "P4ss-xyz") {
		t.Fatal("el log del panic contiene la contraseña")
	}
}
