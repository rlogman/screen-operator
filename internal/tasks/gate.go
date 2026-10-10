package tasks

import (
	"context"
	"errors"
	"sync"
)

// ErrInputClosed indica que la tarea ya reporto, se agoto su tiempo o fue
// cancelada: ningun evento mas llega al escritorio.
var ErrInputClosed = errors.New("entrada cerrada: la tarea ya reporto o fue cancelada")

// ErrNoDriver lo devuelve la sesion por defecto cuando no hay driver conectado.
var ErrNoDriver = errors.New("sin driver de escritorio")

// Gate es la unica puerta de entrada de eventos al escritorio durante una tarea.
// Rechaza envios con el contexto de la tarea cancelado o despues de cerrarse.
// close espera a que termine un envio en curso, asi que cuando close retorna
// el driver ya no recibe ningun evento de esta tarea.
type Gate struct {
	mu     sync.Mutex
	ctx    context.Context
	sess   Session
	closed bool
	sent   int
	denied int
}

func newGate(ctx context.Context, sess Session) *Gate {
	return &Gate{ctx: ctx, sess: sess}
}

// Send entrega ev al driver si la tarea sigue activa.
func (g *Gate) Send(ev Event) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.ctx.Err() != nil {
		g.denied++
		return ErrInputClosed
	}
	if err := g.sess.Send(g.ctx, ev); err != nil {
		return err
	}
	g.sent++
	return nil
}

// Close cierra la puerta; desde que retorna, Send siempre falla con ErrInputClosed.
func (g *Gate) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// Counts devuelve eventos entregados y rechazados.
func (g *Gate) Counts() (sent, denied int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sent, g.denied
}

// noDriverSession es la sesion cuando el Task Manager no tiene Dialer.
type noDriverSession struct{}

func (noDriverSession) Send(context.Context, Event) error { return ErrNoDriver }
func (noDriverSession) Close() error                      { return nil }
