package fakes

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"screenoperator/internal/llm"
)

// ErrScriptExhausted lo devuelve ScriptedLLM cuando se acabo el guion y no hay
// respuesta por defecto.
var ErrScriptExhausted = errors.New("fakes: guion del LLM agotado")

// Reply es una respuesta guionada. Si Func no es nil, decide la respuesta a
// partir del request (permite mirar el prompt).
type Reply struct {
	Content string
	Err     error
	Usage   llm.Usage
	Func    func(req llm.Request) (string, error)
}

// ScriptedLLM es un llm.Chatter que responde en orden un guion y registra
// cada request. Es seguro para uso concurrente.
type ScriptedLLM struct {
	mu       sync.Mutex
	script   []Reply
	Default  *Reply // respuesta cuando se acaba el guion (nil = error)
	Requests []llm.Request
}

// NewScriptedLLM crea el LLM con respuestas de texto.
func NewScriptedLLM(contents ...string) *ScriptedLLM {
	s := &ScriptedLLM{}
	for _, c := range contents {
		s.script = append(s.script, Reply{Content: c})
	}
	return s
}

// Push agrega respuestas al final del guion.
func (s *ScriptedLLM) Push(r ...Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script = append(s.script, r...)
}

// Chat implementa llm.Chatter.
func (s *ScriptedLLM) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	s.mu.Lock()
	s.Requests = append(s.Requests, req)
	var r Reply
	switch {
	case len(s.script) > 0:
		r = s.script[0]
		s.script = s.script[1:]
	case s.Default != nil:
		r = *s.Default
	default:
		s.mu.Unlock()
		return llm.Response{}, fmt.Errorf("%w (request %d, %s)", ErrScriptExhausted, len(s.Requests), req.Purpose)
	}
	s.mu.Unlock()
	if r.Func != nil {
		c, err := r.Func(req)
		if err != nil {
			return llm.Response{}, err
		}
		r.Content = c
	}
	if r.Err != nil {
		return llm.Response{}, r.Err
	}
	u := r.Usage
	if u.PromptTokens == 0 && u.CompletionTokens == 0 {
		u = llm.Usage{PromptTokens: 1500, CompletionTokens: 60, CostUSD: 0.00018}
	}
	return llm.Response{Content: r.Content, Model: req.Model, Usage: u}, nil
}

// Calls devuelve una copia de los requests recibidos.
func (s *ScriptedLLM) Calls() []llm.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llm.Request(nil), s.Requests...)
}
