package client

import (
	"context"
	"sync"
)

// authGate preserves concurrent requests and exclusive credential rotation.
// Unlike sync.RWMutex, both kinds of wait honor the caller's cancellation.
type authGate struct {
	mu      sync.Mutex
	readers int
	writers int
	writing bool
	changed chan struct{}
}

func (g *authGate) notify() {
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
}

func (g *authGate) acquire(ctx context.Context, write bool) (func(), error) {
	g.mu.Lock()
	if write {
		g.writers++
	}
	for {
		if err := ctx.Err(); err != nil {
			if write {
				g.writers--
				g.notify()
			}
			g.mu.Unlock()
			return nil, err
		}
		if !g.writing && (write && g.readers == 0 || !write && g.writers == 0) {
			if write {
				g.writers--
				g.writing = true
			} else {
				g.readers++
			}
			g.mu.Unlock()
			return func() {
				g.mu.Lock()
				if write {
					g.writing = false
				} else {
					g.readers--
				}
				g.notify()
				g.mu.Unlock()
			}, nil
		}
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		g.mu.Lock()
	}
}
