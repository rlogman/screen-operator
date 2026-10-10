package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/tasks"
)

// --- 3.3 GET /tasks/{task_id} ---

func TestE3_3_S1_NewTaskIsRunning(t *testing.T) {
	r := newBlockRunner()
	h := newHarness(t, r, tasks.Budget{})
	t.Cleanup(func() { close(r.release) })
	id := h.create(validBody)
	code, m := h.status(id)
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	if m["estado"] != "en_curso" {
		t.Fatalf("estado = %v, se esperaba en_curso", m["estado"])
	}
	if _, ok := m["resumen"].(string); !ok {
		t.Fatalf("resumen no es string: %#v", m["resumen"])
	}
}

func TestE3_3_S2_DoneTaskShowsSummary(t *testing.T) {
	r := newBlockRunner()
	h := newHarness(t, r, tasks.Budget{})
	id := h.create(validBody)
	<-r.started
	close(r.release)
	m := h.waitFinal(id, 5*time.Second)
	if m["estado"] != "terminada" || m["resumen"] != "respuesta: 45" {
		t.Fatalf("respuesta = %v, se esperaba terminada / 'respuesta: 45'", m)
	}
}

func TestE3_3_S3_UnknownIDIs404(t *testing.T) {
	h := newHarness(t, newBlockRunner(), tasks.Budget{})
	for _, id := range []string{"no-existe", "abc", "0000"} {
		code, _ := h.status(id)
		if code != http.StatusNotFound {
			t.Fatalf("GET /tasks/%s = %d, se esperaba 404", id, code)
		}
	}
}

func TestE3_3_S4_ResponseHasExactlyEstadoResumen(t *testing.T) {
	r := newBlockRunner()
	h := newHarness(t, r, tasks.Budget{})
	keys := func(m map[string]any) string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	id := h.create(validBody)
	_, running := h.status(id)
	<-r.started
	close(r.release)
	done := h.waitFinal(id, 5*time.Second)
	_, missing := h.status("desconocida")
	// Fallida por timeout.
	h2 := newHarness(t, tasks.RunnerFunc(func(ctx context.Context, _ *tasks.Job) tasks.Result {
		<-ctx.Done()
		return tasks.Result{Status: tasks.StatusDone}
	}), tasks.Budget{Total: 1500 * time.Millisecond, Margin: 500 * time.Millisecond})
	failed := h2.waitFinal(h2.create(validBody), 5*time.Second)
	for name, m := range map[string]map[string]any{"en_curso": running, "terminada": done, "404": missing, "fallida": failed} {
		if got := keys(m); got != "estado,resumen" {
			t.Fatalf("%s: claves = %q, se esperaba exactamente estado,resumen", name, got)
		}
		if _, ok := m["estado"].(string); !ok {
			t.Fatalf("%s: estado no es string", name)
		}
		if _, ok := m["resumen"].(string); !ok {
			t.Fatalf("%s: resumen no es string", name)
		}
	}
	if failed["estado"] != "fallida" {
		t.Fatalf("estado = %v", failed["estado"])
	}
}
