package session

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/authorization"
	"netgo/internal/gateway"
	"netgo/internal/model"
	"netgo/internal/store"
	"reflect"
	"sort"
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
	mu           sync.Mutex
	Store        *store.Store
	Gate         gateway.Gate
	Peers        Peers
	TTL          time.Duration
	Now          func() time.Time
	applied      []model.Lease
	appliedBytes int
	lastApply    time.Time
	stopped      bool
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
// The gate call deliberately remains within the commit fence: moving it outside
// would let a concurrent credential revocation race with newly installed access.
// All remote peer/account IO happens before entering this critical section.
func (m *Manager) update(ctx context.Context, fn func(*model.State) error) error {
	if m.stopped {
		return ErrStopped
	}
	return m.commit(ctx, fn)
}

func (m *Manager) commit(ctx context.Context, fn func(*model.State) error) error {
	return m.commitWith(ctx, fn, m.Store.Update)
}

func (m *Manager) vpnUpdate(ctx context.Context, fn func(*model.State) error) error {
	if m.stopped {
		return ErrStopped
	}
	return m.commitWith(ctx, fn, m.Store.VPNUpdate)
}

func (m *Manager) commitWith(ctx context.Context, fn func(*model.State) error, write func(func(*model.State) error) error) error {
	touched := false
	e := write(func(st *model.State) error {
		if e := fn(st); e != nil {
			return e
		}
		leases := active(st, m.now())
		if e := gateway.ValidateCapacity(leases); e != nil {
			return e
		}
		sort.Slice(leases, func(i, j int) bool { return leases[i].ID < leases[j].ID })
		if !m.lastApply.IsZero() && m.now().Sub(m.lastApply) < time.Minute && reflect.DeepEqual(m.applied, leases) {
			return nil
		}
		touched = true
		if e := m.Gate.Apply(ctx, leases); e != nil {
			return e
		}
		m.rememberApplied(leases)
		return nil
	})
	if e != nil && touched {
		e = errors.Join(e, m.failClosed())
	}
	return e
}
func (m *Manager) Acquire(ctx context.Context, c model.Credential, peer string, generation uint64, replace bool, key string) (model.Lease, error) {
	var result model.Lease
	if key == "" || len(key) > 128 || generation == 0 {
		return result, errors.New("idempotency key and generation required")
	}
	p, e := m.Peers.Verify(ctx, peer)
	if e != nil {
		return result, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return result, e
	}
	now := m.now()
	fp := model.Hash(fmt.Sprintf("%s|%s|%s|%d|%t", c.UserID, c.DeviceID, peer, generation, replace))
	opKey := model.Hash(c.UserID + ":" + key)
	e = m.vpnUpdate(ctx, func(st *model.State) error {
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
		// Overlay admission is independent of LAN access. An empty resource set
		// installs no gateway allowance; remote sessions still require a grant.
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
	var result model.Lease
	prior, e := m.Store.Lease(id)
	if e != nil {
		return result, e
	}
	if prior.UserID != c.UserID || prior.DeviceID != c.DeviceID {
		return result, model.ErrForbidden
	}
	p, e := m.Peers.Verify(ctx, prior.PeerID)
	if e != nil {
		return result, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return result, e
	}
	now := m.now()
	if m.stopped {
		return result, ErrStopped
	}
	touched := false
	e = m.Store.VPNRenew(c, id, now, func(st *model.State) error {
		if !credentialCurrent(st, c, m.now()) {
			return model.ErrUnauthorized
		}
		l, ok := st.Leases[id]
		u := st.Users[c.UserID]
		if !ok || l.UserID != c.UserID || l.DeviceID != c.DeviceID || l.Generation != generation || !now.Before(l.ExpiresAt) || !now.Before(c.ExpiresAt) || !u.Active(now) || st.Devices[c.DeviceID].DeletePending || st.Devices[c.DeviceID].UserID != c.UserID {
			return model.ErrForbidden
		}
		if l.PeerID != prior.PeerID || p.ID != l.PeerID || p.UserID != l.UserID || p.IP != l.IP || p.PublicKey != st.Devices[c.DeviceID].WireGuardKey || p.Expired || !p.Connected {
			return model.ErrForbidden
		}
		l.ExpiresAt = now.Add(m.TTL)
		if !u.ExpiresAt.IsZero() && u.ExpiresAt.Before(l.ExpiresAt) {
			l.ExpiresAt = u.ExpiresAt
		}
		if c.ExpiresAt.Before(l.ExpiresAt) {
			l.ExpiresAt = c.ExpiresAt
		}
		l.Resources = []model.Resource{}
		for _, rid := range u.Resources {
			if r, ok := st.Resources[rid]; ok && r.Enabled {
				l.Resources = append(l.Resources, r)
			}
		}
		st.Leases[id] = l
		result = l
		return nil
	}, store.RenewalGate{Refresh: func(next model.Lease) (bool, error) {
		handled, err := m.refreshLease(ctx, next)
		// Capacity rejection did not touch the gate. Any other error may have.
		touched = handled || (err != nil && !errors.Is(err, gateway.ErrCapacity))
		return handled, err
	}, Apply: func(leases []model.Lease) error {
		if err := gateway.ValidateCapacity(leases); err != nil {
			return err
		}
		sort.Slice(leases, func(i, j int) bool { return leases[i].ID < leases[j].ID })
		touched = true
		if err := m.Gate.Apply(ctx, leases); err != nil {
			return err
		}
		m.rememberApplied(leases)
		return nil
	}})
	if e != nil && touched {
		e = errors.Join(e, m.failClosed())
	}
	return result, e
}
func (m *Manager) Revoke(ctx context.Context, actor, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vpnUpdate(ctx, func(st *model.State) error {
		l, ok := st.Leases[id]
		if !ok {
			return nil
		}
		if actor != l.UserID && actor != "admin" {
			return model.ErrForbidden
		}
		if actor == "admin" {
			if err := authorization.CheckAdmin(ctx, st); err != nil {
				return err
			}
			actor = authorization.Actor(ctx)
		}
		delete(st.Leases, id)
		st.Record(actor, "session.revoke", id)
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
	if !m.lastApply.IsZero() && m.now().Sub(m.lastApply) < time.Minute {
		due, err := m.Store.SessionCleanupDue(m.now())
		if err != nil || !due {
			return err
		}
	}
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
