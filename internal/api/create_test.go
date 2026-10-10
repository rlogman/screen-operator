package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"screenoperator/internal/tasks"
)

// --- 3.2 POST /tasks ---

func TestE3_2_S1_CreateRespondsFastWithTaskID(t *testing.T) {
	r := newBlockRunner() // el agente "tarda minutos": no termina hasta el cleanup
	h := newHarness(t, r, tasks.Budget{})
	t.Cleanup(func() { close(r.release) })
	for i := 0; i < 5; i++ {
		start := time.Now()
		res, body := h.do(http.MethodPost, "/tasks", "Bearer "+testToken, validBody)
		el := time.Since(start)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("POST /tasks = %d %s", res.StatusCode, body)
		}
		if el >= 300*time.Millisecond {
			t.Fatalf("POST /tasks tardo %v, limite 300 ms", el)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		id, _ := m["task_id"].(string)
		if id == "" || len(m) != 1 {
			t.Fatalf("cuerpo = %s, se esperaba {\"task_id\": \"...\"}", body)
		}
		if !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("Content-Type = %q", res.Header.Get("Content-Type"))
		}
	}
}

func TestE3_2_S2_MissingFieldIs400NamingIt(t *testing.T) {
	h := newHarness(t, newBlockRunner(), tasks.Budget{})
	cases := []struct{ body, field string }{
		{`{"escritorio":{"url":"wss://d/websockify","password":"p"}}`, "tarea"},
		{`{"tarea":"","escritorio":{"url":"wss://d/websockify","password":"p"}}`, "tarea"},
		{`{"tarea":"   ","escritorio":{"url":"wss://d/websockify","password":"p"}}`, "tarea"},
		{`{"tarea":7,"escritorio":{"url":"wss://d/websockify","password":"p"}}`, "tarea"},
		{`{"tarea":"x","escritorio":{"url":"","password":"p"}}`, "escritorio.url"},
		{`{"tarea":"x","escritorio":{"password":"p"}}`, "escritorio.url"},
		{`{"tarea":"x"}`, "escritorio.url"},
		{`{"tarea":"x","escritorio":{"url":"wss://d/websockify","password":5}}`, "escritorio.password"},
	}
	for _, tc := range cases {
		res, body := h.do(http.MethodPost, "/tasks", "Bearer "+testToken, tc.body)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d, se esperaba 400", tc.body, res.StatusCode)
		}
		if !strings.Contains(string(body), "'"+tc.field+"'") {
			t.Fatalf("%s: el mensaje %s no nombra %q", tc.body, body, tc.field)
		}
	}
}

func TestE3_2_S3_InvalidJSONIs400(t *testing.T) {
	h := newHarness(t, newBlockRunner(), tasks.Budget{})
	for _, b := range []string{`{"tarea":`, `no es json`, `[1,2]`, `"texto"`, validBody + `{}`, ``} {
		res, body := h.do(http.MethodPost, "/tasks", "Bearer "+testToken, b)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%q = %d %s, se esperaba 400", b, res.StatusCode, body)
		}
	}
	big := `{"tarea":"` + strings.Repeat("a", MaxBodyBytes) + `","escritorio":{"url":"wss://d/websockify"}}`
	if res, _ := h.do(http.MethodPost, "/tasks", "Bearer "+testToken, big); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("cuerpo de mas de 1 MiB = %d, se esperaba 400", res.StatusCode)
	}
}

func TestE3_2_S4_SameBodyTwiceDistinctIDs(t *testing.T) {
	r := newBlockRunner()
	h := newHarness(t, r, tasks.Budget{})
	t.Cleanup(func() { close(r.release) })
	a := h.create(validBody)
	b := h.create(validBody)
	if a == b {
		t.Fatalf("dos POST devolvieron el mismo task_id %q", a)
	}
}
