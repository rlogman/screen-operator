package api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"screenoperator/internal/tasks"
)

// MaxBodyBytes limita el cuerpo de POST /tasks.
const MaxBodyBytes = 1 << 20

// TaskService es lo que la API necesita del Task Manager.
type TaskService interface {
	Submit(req tasks.Request) (string, error)
	Get(id string) (tasks.Snapshot, bool)
}

// Server sirve el contrato del evento.
type Server struct {
	c     Contract
	token [sha256.Size]byte
	svc   TaskService
	log   *slog.Logger
	mux   *http.ServeMux
}

// NewServer crea el handler del contrato. token es el valor de TEAM_TOKEN y no
// puede ser vacio.
func NewServer(c Contract, token string, svc TaskService, log *slog.Logger) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("api: falta el token (%s)", c.TokenEnv)
	}
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	s := &Server{c: c, token: sha256.Sum256([]byte(token)), svc: svc, log: log, mux: http.NewServeMux()}
	// /health no exige token: lo usan los healthchecks de la plataforma
	// (decision registrada en screen-operator-v1.decisions.md, historia 3.1).
	s.mux.HandleFunc(c.Health.Method+" "+c.Health.Path, s.health)
	s.mux.Handle(c.Create.Method+" "+c.Create.Path, s.auth(http.HandlerFunc(s.create)))
	s.mux.Handle(c.Get.Method+" "+c.Get.Path, s.auth(http.HandlerFunc(s.get)))
	return s, nil
}

// ServeHTTP implementa http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
	s.mux.ServeHTTP(rw, r)
	attrs := []any{tasks.LogKeyPhase, "http", "metodo", r.Method, "ruta", r.URL.Path,
		"codigo", rw.code, "duracion_ms", time.Since(start).Milliseconds()}
	if rw.taskID != "" {
		attrs = append(attrs, tasks.LogKeyTaskID, rw.taskID)
	}
	s.log.Info("http", attrs...)
}

type statusWriter struct {
	http.ResponseWriter
	code   int
	taskID string
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func setTaskID(w http.ResponseWriter, id string) {
	if sw, ok := w.(*statusWriter); ok {
		sw.taskID = id
	}
}

// auth exige "Authorization: Bearer <token>" y compara en tiempo constante.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r.Header.Get("Authorization")) {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no autorizado"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(h string) bool {
	scheme, tok, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	got := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	return subtle.ConstantTimeCompare(got[:], s.token[:]) == 1
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cuerpo ilegible o mayor a 1 MiB"})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON invalido"})
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON invalido: contenido despues del objeto"})
		return
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON invalido: se esperaba un objeto"})
		return
	}
	instr, err := requiredString(obj, s.c.TaskField)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	desk, err := requiredString(obj, s.c.DesktopURLField)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req := tasks.Request{Instruction: instr, DesktopURL: strings.TrimSpace(desk)}
	for _, f := range s.c.DesktopSecretFields {
		v, present := lookup(obj, f)
		if !present || v == nil {
			req.DesktopSecrets = append(req.DesktopSecrets, "")
			continue
		}
		sv, ok := v.(string)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("el campo '%s' debe ser texto", f)})
			return
		}
		req.DesktopSecrets = append(req.DesktopSecrets, tasks.Secret(sv))
	}
	id, err := s.svc.Submit(req)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "el agente no acepta tareas en este momento"})
		return
	}
	setTaskID(w, id)
	writeJSON(w, http.StatusOK, map[string]string{s.c.IDField: id})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(s.c.getParam())
	snap, ok := s.svc.Get(id)
	if !ok {
		// Mismas claves que una respuesta normal: un cliente que lee estado y
		// resumen nunca recibe otra forma.
		writeJSON(w, http.StatusNotFound, map[string]string{
			s.c.StatusField:  s.c.StatusValues[tasks.StatusFailed],
			s.c.SummaryField: "Tarea desconocida para este agente.",
		})
		return
	}
	setTaskID(w, id)
	writeJSON(w, http.StatusOK, map[string]string{
		s.c.StatusField:  s.c.StatusValues[snap.Status],
		s.c.SummaryField: snap.Summary,
	})
}

// requiredString exige un texto no vacio en la ruta con puntos f.
func requiredString(obj map[string]any, f string) (string, error) {
	v, ok := lookup(obj, f)
	if !ok || v == nil {
		return "", fmt.Errorf("falta el campo '%s'", f)
	}
	sv, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("el campo '%s' debe ser texto", f)
	}
	if strings.TrimSpace(sv) == "" {
		return "", fmt.Errorf("el campo '%s' esta vacio", f)
	}
	return sv, nil
}

// lookup sigue una ruta con puntos ("escritorio.url") en un objeto JSON.
func lookup(obj map[string]any, path string) (any, bool) {
	var cur any = obj
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
