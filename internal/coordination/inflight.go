// Package coordination owns short, nonblocking reservations for external work.
package coordination

import (
	"errors"
	"sync"
)

var ErrBusy = errors.New("operation already in progress")

// InFlight reserves related keys atomically. It holds no lock during IO and
// removes idle keys, so neither a slow operation nor historical IDs accumulate.
type InFlight struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (f *InFlight) Try(keys ...string) (func(), bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, key := range keys {
		if f.keys[key] {
			return nil, false
		}
	}
	if f.keys == nil {
		f.keys = make(map[string]bool)
	}
	for _, key := range keys {
		f.keys[key] = true
	}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, key := range keys {
			delete(f.keys, key)
		}
	}, true
}
