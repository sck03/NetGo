package session

import (
	"context"
	"errors"
	"netgo/internal/model"
	"time"
)

var ErrStopped = errors.New("session_manager_stopped")

// Reset clears runtime access at startup. It never reopens a stopped manager.
func (m *Manager) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrStopped
	}
	return m.reset(ctx)
}

// Close fences every mutation before clearing access, including requests which
// are still verifying peers. It can be retried if the gate is unavailable.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return m.reset(ctx)
}

func (m *Manager) reset(ctx context.Context) error {
	m.lastApply = time.Time{}
	return m.commit(ctx, func(st *model.State) error {
		st.Leases = map[string]model.Lease{}
		st.Operations = map[string]model.Operation{}
		return nil
	})
}

// A failed external apply or commit must not leave uncommitted access open.
// Called with the manager mutex held, after the SQL transaction has rolled back.
func (m *Manager) failClosed() error {
	m.lastApply = time.Time{}
	m.applied = nil
	cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	gateErr := m.Gate.Apply(cleanup, nil)
	storeErr := m.Store.Update(func(st *model.State) error {
		st.Leases = map[string]model.Lease{}
		st.Operations = map[string]model.Operation{}
		return nil
	})
	return errors.Join(gateErr, storeErr)
}
