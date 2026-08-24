package agentloop

import (
	"context"
	"sync"
)

type engineFlight struct {
	target string
	done   chan struct{}
	err    error
}

type engineGate struct {
	mu     sync.Mutex
	flight *engineFlight
}

func newEngineGate() *engineGate {
	return &engineGate{}
}

func (g *engineGate) AwaitTransition(ctx context.Context, target string, run func(context.Context) error) error {
	g.mu.Lock()
	cur := g.flight
	switch {
	case cur != nil && cur.target == target && cur.done != nil:
		g.mu.Unlock()
		return g.joinFlight(ctx, cur)
	default:
		flight := &engineFlight{target: target, done: make(chan struct{})}
		g.flight = flight
		g.mu.Unlock()
		defer g.finishFlight(target, flight)
		flight.err = run(ctx)
		return flight.err
	}
}

func (g *engineGate) joinFlight(ctx context.Context, flight *engineFlight) error {
	select {
	case <-flight.done:
		return flight.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *engineGate) finishFlight(target string, flight *engineFlight) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.flight == flight {
		g.flight = nil
	}
	close(flight.done)
}
