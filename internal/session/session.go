package session

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/gateway"
	"netgo/internal/model"
	"netgo/internal/store"
	"sync"
	"time"
)

type Binding struct {
	ID, UserID, IP, PublicKey string
	Connected, Expired        bool
}
type Peers interface {
	Verify(context.Context, string) (Binding, error)
}
type Manager struct {
	mu    sync.Mutex
	Store *store.Store
	Gate  gateway.Gate
	Peers Peers
	TTL   time.Duration
	Now   func() time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now().UTC()
}
func active(st *model.State, now time.Time) []model.Lease {
	out := []model.Lease{}
	for id, l := range st.Leases {
		u, ok := st.Users[l.UserID]
		if !ok || !u.Active(now) || !now.Before(l.ExpiresAt) {
			delete(st.Leases, id)
			continue
		}
		out = append(out, l)
	}
	return out
}

func credentialCurrent(st *model.State, c model.Credential, now time.Time) bool {
	stored, ok := st.Credentials[c.TokenHash]
	return c.TokenHash != "" && ok && stored.UserID == c.UserID && stored.DeviceID == c.DeviceID &&
		stored.ExpiresAt.Equal(c.ExpiresAt) && now.Before(stored.ExpiresAt)
}

// Only a validated state transition touches the gate. The nft transaction
// atomically removes old leases and installs new ones, with no overlap window.
func (m *Manager) update(ctx context.Context, fn func(*model.State) error) error {
	touched := false
	e := m.Store.Update(func(st *model.State) error {
		if e := fn(st); e != nil {
			return e
		}
		touched = true
		return m.Gate.Apply(ctx, active(st, m.now()))
	})
	if e != nil && touched {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = m.Gate.Apply(cleanup, nil)
		_ = m.Store.Update(func(st *model.State) error {
			st.Leases = map[string]model.Lease{}
			st.Operations = map[string]model.Operation{}
			return nil
		})
	}
	return e
}
func (m *Manager) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, func(st *model.State) error {
		st.Leases = map[string]model.Lease{}
		st.Operations = map[string]model.Operation{}
		return nil
	})
}
func (m *Manager) Acquire(ctx context.Context, c model.Credential, peer string, generation uint64, replace bool, key string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result model.Lease
	if key == "" || len(key) > 128 || generation == 0 {
		return result, errors.New("idempotency key and generation required")
	}
	p, e := m.Peers.Verify(ctx, peer)
	if e != nil {
		return result, e
	}
	now := m.now()
	fp := model.Hash(fmt.Sprintf("%s|%s|%s|%d|%t", c.UserID, c.DeviceID, peer, generation, replace))
	opKey := model.Hash(c.UserID + ":" + key)
	e = m.update(ctx, func(st *model.State) error {
		// Authorization may have been revoked while reconciliation or peer lookup ran.
		if !credentialCurrent(st, c, m.now()) {
			return model.ErrUnauthorized
		}
		u, ok := st.Users[c.UserID]
		d, dk := st.Devices[c.DeviceID]
		if !ok || !dk || d.ClientType == "admin" || !u.Active(now) || !now.Before(c.ExpiresAt) || d.UserID != u.ID || p.UserID != u.ID || p.PublicKey != d.WireGuardKey || p.Expired || !p.Connected {
			return model.ErrForbidden
		}
		if op, ok := st.Operations[opKey]; ok && now.Before(op.ExpiresAt) {
			if op.Fingerprint != fp {
				return model.ErrConflict
			}
			l, ok := st.Leases[op.LeaseID]
			if !ok || !now.Before(l.ExpiresAt) {
				return model.ErrConflict
			}
			result = l
			return nil
		}
		active(st, now)
		limit := u.MaxSessions
		if limit == 0 {
			limit = 1
		} // Unspecified limits default to one active session.
		if limit < 1 || limit > 100 {
			return errors.New("invalid session limit")
		}
		count := 0
		for id, l := range st.Leases {
			if l.UserID != u.ID {
				continue
			}
			if l.DeviceID == d.ID {
				if generation <= l.Generation {
					return model.ErrConflict
				}
				// A newly authenticated restart of the same key-bound device
				// replaces only its older generation, without disturbing others.
				delete(st.Leases, id)
				continue
			}
			count++
		}
		if count >= limit && !replace {
			return model.ErrConflict
		}
		if replace {
			for id, l := range st.Leases {
				if l.UserID == u.ID {
					delete(st.Leases, id)
				}
			}
		}
		resources := []model.Resource{}
		for _, id := range u.Resources {
			if r, ok := st.Resources[id]; ok && r.Enabled {
				resources = append(resources, r)
			}
		}
		if len(resources) == 0 {
			return model.ErrForbidden
		}
		if m.TTL <= 0 || m.TTL > 120*time.Second {
			return errors.New("invalid lease TTL")
		}
		expiry := now.Add(m.TTL)
		if !u.ExpiresAt.IsZero() && u.ExpiresAt.Before(expiry) {
			expiry = u.ExpiresAt
		}
		if c.ExpiresAt.Before(expiry) {
			expiry = c.ExpiresAt
		}
		result = model.Lease{ID: model.ID(), UserID: u.ID, DeviceID: d.ID, PeerID: peer, IP: p.IP, Generation: generation, ExpiresAt: expiry, Resources: resources}
		st.Leases[result.ID] = result
		d.PeerID = peer
		d.IP = p.IP
		st.Devices[d.ID] = d
		st.Operations[opKey] = model.Operation{Fingerprint: fp, LeaseID: result.ID, ExpiresAt: now.Add(10 * time.Minute)}
		st.Record(u.ID, "session.acquire", result.ID)
		return nil
	})
	return result, e
}
func (m *Manager) Renew(ctx context.Context, c model.Credential, id string, generation uint64) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result model.Lease
	now := m.now()
	e := m.update(ctx, func(st *model.State) error {
		if !credentialCurrent(st, c, m.now()) {
			return model.ErrUnauthorized
		}
		l, ok := st.Leases[id]
		u := st.Users[c.UserID]
		if !ok || l.UserID != c.UserID || l.DeviceID != c.DeviceID || l.Generation != generation || !now.Before(l.ExpiresAt) || !now.Before(c.ExpiresAt) || !u.Active(now) {
			return model.ErrForbidden
		}
		p, e := m.Peers.Verify(ctx, l.PeerID)
		if e != nil || p.UserID != l.UserID || p.IP != l.IP || p.PublicKey != st.Devices[c.DeviceID].WireGuardKey || p.Expired {
			return model.ErrForbidden
		}
		l.ExpiresAt = now.Add(m.TTL)
		if !u.ExpiresAt.IsZero() && u.ExpiresAt.Before(l.ExpiresAt) {
			l.ExpiresAt = u.ExpiresAt
		}
		if c.ExpiresAt.Before(l.ExpiresAt) {
			l.ExpiresAt = c.ExpiresAt
		}
		l.Resources = nil
		for _, rid := range u.Resources {
			if r, ok := st.Resources[rid]; ok && r.Enabled {
				l.Resources = append(l.Resources, r)
			}
		}
		if len(l.Resources) == 0 {
			return model.ErrForbidden
		}
		st.Leases[id] = l
		result = l
		return nil
	})
	return result, e
}
func (m *Manager) Revoke(ctx context.Context, actor, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, func(st *model.State) error {
		l, ok := st.Leases[id]
		if !ok {
			return nil
		}
		if actor != l.UserID && actor != "admin" {
			return model.ErrForbidden
		}
		delete(st.Leases, id)
		st.Record(actor, "session.revoke", id)
		return nil
	})
}
func (m *Manager) Mutate(ctx context.Context, fn func(*model.State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, func(st *model.State) error {
		if e := fn(st); e != nil {
			return e
		}
		st.Leases = map[string]model.Lease{}
		return nil
	})
}

// Scoped mutations revoke the affected access without disconnecting other users.
func (m *Manager) MutateUser(ctx context.Context, user string, fn func(*model.State) error) error {
	return m.mutateScope(ctx, func(l model.Lease) bool { return l.UserID == user }, fn)
}
func (m *Manager) MutateResource(ctx context.Context, resource string, fn func(*model.State) error) error {
	return m.mutateScope(ctx, func(l model.Lease) bool {
		for _, r := range l.Resources {
			if r.ID == resource {
				return true
			}
		}
		return false
	}, fn)
}
func (m *Manager) MutateDevice(ctx context.Context, device string, fn func(*model.State) error) error {
	return m.mutateScope(ctx, func(l model.Lease) bool { return l.DeviceID == device }, fn)
}
func (m *Manager) mutateScope(ctx context.Context, matches func(model.Lease) bool, fn func(*model.State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, func(st *model.State) error {
		if err := fn(st); err != nil {
			return err
		}
		for id, l := range st.Leases {
			if matches(l) {
				delete(st.Leases, id)
			}
		}
		return nil
	})
}
func (m *Manager) Sweep(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, func(st *model.State) error {
		now := m.now()
		for k, o := range st.Operations {
			if !now.Before(o.ExpiresAt) {
				delete(st.Operations, k)
			}
		}
		for k, c := range st.Credentials {
			if !now.Before(c.ExpiresAt) {
				delete(st.Credentials, k)
			}
		}
		retained := map[string]bool{}
		for _, credential := range st.Credentials {
			retained[credential.DeviceID] = true
		}
		for id, device := range st.Devices {
			if device.ClientType == "admin" && !retained[id] {
				delete(st.Devices, id)
			}
		}
		return nil
	})
}
