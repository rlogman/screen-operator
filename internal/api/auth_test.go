package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"screenoperator/internal/tasks"
)

// --- 3.1 Servidor HTTP, autenticacion y /health ---

func TestE3_1_S1_HealthWithTokenOK(t *testing.T) {
	h := newHarness(t, newBlockRunner(), tasks.Budget{})
	res, body := h.do(http.MethodGet, "/health", "Bearer "+testToken, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /health = %d %s", res.StatusCode, body)
	}
	var m map[string]bool
	if err := json.Unmarshal(body, &m); err != nil || !m["ok"] {
		t.Fatalf("cuerpo = %s", body)
	}
	// Decision 3.1: /health tambien responde 200 sin token (healthchecks de la plataforma).
	if res, _ := h.do(http.MethodGet, "/health", "", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("GET /health sin token = %d", res.StatusCode)
	}
}

func TestE3_1_S2_TasksWithoutOrWrongTokenIs401(t *testing.T) {
	h := newHarness(t, newBlockRunner(), tasks.Budget{})
	for _, auth := range []string{"", "Bearer otro", "Bearer t0k0", "Bearer t0", "Basic t0k", "t0k", "Bearer"} {
		for _, rq := range []struct{ method, path, body string }{
			{http.MethodGet, "/tasks/abc", ""},
			{http.MethodPost, "/tasks", validBody},
		} {
			res, body := h.do(rq.method, rq.path, auth, rq.body)
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s %s con Authorization %q = %d, se esperaba 401", rq.method, rq.path, auth, res.StatusCode)
			}
			if strings.Contains(string(body), testToken) || strings.Contains(res.Header.Get("WWW-Authenticate"), testToken) {
				t.Fatalf("la respuesta 401 revela el token: %s", body)
			}
		}
	}
	if strings.Contains(h.log.String(), testToken) {
		t.Fatal("los logs revelan el token")
	}
	// Control: con el token correcto, abc es 404 (no 401).
	if res, _ := h.do(http.MethodGet, "/tasks/abc", "Bearer "+testToken, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("con token correcto = %d, se esperaba 404", res.StatusCode)
	}
	// El esquema Bearer no distingue mayusculas.
	if res, _ := h.do(http.MethodGet, "/tasks/abc", "bearer "+testToken, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("'bearer' en minusculas = %d", res.StatusCode)
	}
}

func TestNewServerRequiresToken(t *testing.T) {
	if _, err := NewServer(repoContract(t), "", nil, nil); err == nil || !strings.Contains(err.Error(), "TEAM_TOKEN") {
		t.Fatalf("se esperaba error que nombra TEAM_TOKEN, fue %v", err)
	}
}
