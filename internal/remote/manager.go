// Package remote coordinates authorization. It owns no sockets, media, platform
// SDKs or account system; all sensitive mutations recheck the shared identity.
package remote

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"math"
	"net"
	"netgo/internal/model"
	"netgo/internal/remote/ticket"
	"netgo/internal/session"
	"netgo/internal/store"
	"strconv"
	"strings"
	"time"
)

const LeaseDuration = 30 * time.Second
const HeartbeatTimeout = 15 * time.Second

// Hosts advertise only capabilities provided by their installed adapters.
const SupportedCapabilities = ticket.CapabilityView | ticket.CapabilityInput | ticket.CapabilityAudio | ticket.CapabilityClipboard | ticket.CapabilityUpload | ticket.CapabilityDownload

var ErrUnavailable = errors.New("remote_host_unavailable")
var ErrBusy = errors.New("remote_host_busy")
var ErrReplay = errors.New("remote_ticket_replayed")

type Manager struct {
	Store  *store.Store
	SiteID string
	Key    ed25519.PrivateKey
	Peers  session.Peers
	Now    func() time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func (m *Manager) PublicKey() string {
	return base64.RawURLEncoding.EncodeToString(m.Key.Public().(ed25519.PublicKey))
}
func capabilities(c uint32) bool {
	return c&ticket.CapabilityView != 0 && c & ^SupportedCapabilities == 0
}
func decode(s string, n int) ([]byte, error) {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil || len(b) != n || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, model.ErrForbidden
	}
	var nonzero byte
	for _, v := range b {
		nonzero |= v
	}
	if nonzero == 0 {
		return nil, model.ErrForbidden
	}
	return b, nil
}

func current(st *model.State, c model.Credential, now time.Time) error {
	v, ok := st.Credentials[c.TokenHash]
	u, uok := st.Users[c.UserID]
	d, dok := st.Devices[c.DeviceID]
	if c.TokenHash == "" || !ok || !uok || !dok || v.UserID != c.UserID || v.DeviceID != c.DeviceID || !v.ExpiresAt.Equal(c.ExpiresAt) || !now.Before(c.ExpiresAt) || !u.Active(now) || d.UserID != c.UserID {
		return model.ErrUnauthorized
	}
	return nil
}
func activeLease(st *model.State, device string, now time.Time) (model.Lease, bool) {
	d := st.Devices[device]
	for _, l := range st.Leases {
		if l.DeviceID == device && l.UserID == d.UserID && l.PeerID != "" && l.PeerID == d.PeerID && l.IP == d.IP && now.Before(l.ExpiresAt) {
			return l, true
		}
	}
	return model.Lease{}, false
}

func (m *Manager) Register(c model.Credential, h model.Host) (model.Host, error) {
	if _, e := decode(h.Boot, 16); e != nil {
		return h, e
	}
	if _, e := decode(h.TransportKey, 32); e != nil {
		return h, errors.New("invalid_remote_transport_key")
	}
	if h.ID != c.DeviceID || len(strings.TrimSpace(h.Name)) == 0 || len(h.Name) > 128 || !capabilities(h.Capabilities) || h.Port < 1024 || (h.Platform != "windows" && h.Platform != "macos" && h.Platform != "linux") {
		return h, errors.New("invalid_remote_host")
	}
	err := m.Store.RemotePolicyUpdate(func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		if st.Devices[c.DeviceID].ClientType == "admin" {
			return model.ErrForbidden
		}
		if _, ok := activeLease(st, c.DeviceID, now); !ok {
			return ErrUnavailable
		}
		old, exists := st.Hosts[h.ID]
		if old.Revision == math.MaxUint64 {
			return model.ErrConflict
		}
		h.Revision = old.Revision + 1
		h.LastSeen = now
		if exists {
			closeHost(st, h.ID, c.UserID, now)
		}
		st.Hosts[h.ID] = h
		st.Record(c.UserID, "remote.host.register", h.ID)
		return nil
	})
	return h, err
}

func closeHost(st *model.State, id, actor string, now time.Time) {
	for key, r := range st.RemoteSessions {
		if r.HostID == id && r.State != "closed" {
			r.Close(now)
			st.RemoteSessions[key] = r
			st.Record(actor, "remote.session.close", key)
		}
	}
}

func (m *Manager) Heartbeat(c model.Credential, boot string) error {
	return m.Store.RemoteTargetUpdate(c, "", c.DeviceID, func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		h, ok := st.Hosts[c.DeviceID]
		if !ok || h.Boot != boot || !h.Enabled {
			return ErrUnavailable
		}
		if _, ok := activeLease(st, c.DeviceID, now); !ok {
			return ErrUnavailable
		}
		h.LastSeen = now
		st.Hosts[h.ID] = h
		return nil
	})
}

func (m *Manager) Grant(c model.Credential, g model.RemoteGrant) (model.RemoteGrant, error) {
	if !capabilities(g.Capabilities) {
		return g, errors.New("invalid_remote_capabilities")
	}
	g.ID = model.Hash(g.UserID + ":" + g.HostID)
	err := m.Store.RemotePolicyUpdate(func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		if st.Users[c.UserID].Role != "admin" {
			return model.ErrForbidden
		}
		u, ok := st.Users[g.UserID]
		if !ok {
			return model.ErrNotFound
		}
		if !u.Active(now) {
			return errors.New("remote_user_inactive")
		}
		host, ok := st.Hosts[g.HostID]
		if !ok {
			return model.ErrNotFound
		}
		if g.Capabilities & ^host.Capabilities != 0 {
			return errors.New("remote_capabilities_unavailable")
		}
		if !g.ExpiresAt.IsZero() && !g.ExpiresAt.After(now) {
			return errors.New("invalid_remote_grant_expiry")
		}
		old := st.RemoteGrants[g.ID]
		if old.ID == g.ID && old.Capabilities == g.Capabilities && old.ExpiresAt.Equal(g.ExpiresAt) {
			g = old
			return nil
		}
		if old.Revision == math.MaxUint64 {
			return model.ErrConflict
		}
		g.Revision = old.Revision + 1
		st.RemoteGrants[g.ID] = g
		// Any ACL change fences previous tickets; renewal cannot increase authority.
		for id, r := range st.RemoteSessions {
			if r.GrantID == g.ID {
				r.Close(now)
				st.RemoteSessions[id] = r
			}
		}
		st.Record(c.UserID, "remote.grant.put", g.ID)
		return nil
	})
	return g, err
}

func (m *Manager) DeleteGrant(c model.Credential, id string) error {
	return m.Store.RemotePolicyUpdate(func(st *model.State) error {
		if e := current(st, c, m.now()); e != nil {
			return e
		}
		if st.Users[c.UserID].Role != "admin" {
			return model.ErrForbidden
		}
		delete(st.RemoteGrants, id)
		for key, r := range st.RemoteSessions {
			if r.GrantID == id {
				r.Close(m.now())
				st.RemoteSessions[key] = r
			}
		}
		st.Record(c.UserID, "remote.grant.delete", id)
		return nil
	})
}

func (m *Manager) expiry(st *model.State, c model.Credential, cl, hl model.Lease, g model.RemoteGrant, now time.Time) time.Time {
	expires := now.Add(LeaseDuration)
	for _, t := range []time.Time{c.ExpiresAt, cl.ExpiresAt, hl.ExpiresAt, g.ExpiresAt, st.Users[c.UserID].ExpiresAt, st.Users[st.Devices[g.HostID].UserID].ExpiresAt} {
		if !t.IsZero() && t.Before(expires) {
			expires = t
		}
	}
	return expires
}

func (m *Manager) valid(st *model.State, r model.RemoteSession, now time.Time) bool {
	h, hok := st.Hosts[r.HostID]
	g, gok := st.RemoteGrants[r.GrantID]
	c, cok := st.Credentials[r.ControllerAuth]
	c.TokenHash = r.ControllerAuth
	cl, clok := st.Leases[r.ControllerLease]
	hl, hlok := st.Leases[r.HostLease]
	hu, huok := st.Users[st.Devices[r.HostID].UserID]
	return r.State != "closed" && now.Before(r.ExpiresAt) && !now.Before(r.IssuedAt) && hok && h.Enabled && h.Boot == r.HostBoot && now.Sub(h.LastSeen) >= 0 && now.Sub(h.LastSeen) < HeartbeatTimeout && gok && g.Revision == r.PolicyRevision && (g.ExpiresAt.IsZero() || now.Before(g.ExpiresAt)) && r.Capabilities & ^(g.Capabilities&h.Capabilities) == 0 && cok && current(st, c, now) == nil && c.DeviceID == r.ControllerID && clok && hlok && cl.DeviceID == r.ControllerID && hl.DeviceID == r.HostID && now.Before(cl.ExpiresAt) && now.Before(hl.ExpiresAt) && huok && hu.Active(now)
}

func (m *Manager) sweep(st *model.State, now time.Time) {
	for id, r := range st.RemoteSessions {
		if r.State != "closed" && !m.valid(st, r, now) {
			r.Close(now)
			st.RemoteSessions[id] = r
			st.Record("system", "remote.session.expire", id)
		}
		if r.State == "closed" && now.Sub(r.ClosedAt) > 24*time.Hour {
			delete(st.RemoteSessions, id)
		}
	}
	for id, op := range st.RemoteOperations {
		if !now.Before(op.ExpiresAt) {
			delete(st.RemoteOperations, id)
		}
	}
}

func (m *Manager) Sweep() error {
	due, err := m.Store.RemoteCleanupDue(m.now())
	if err != nil || !due {
		return err
	}
	return m.Store.RemoteSweep(m.now(), func(st *model.State) error { m.sweep(st, m.now()); return nil })
}

func (m *Manager) authorization(st *model.State, r model.RemoteSession) (Authorization, error) {
	b, e := decode(r.Binding, 32)
	if e != nil {
		return Authorization{}, e
	}
	id, e := decode(r.ID, 16)
	if e != nil {
		return Authorization{}, e
	}
	wire, e := ticket.Sign(m.Key, ticket.Claims{Binding: [32]byte(b), Session: [16]byte(id), Generation: r.Generation, PolicyRevision: r.PolicyRevision, Sequence: r.Sequence, IssuedMS: uint64(r.IssuedAt.UnixMilli()), ExpiresMS: uint64(r.ExpiresAt.UnixMilli()), Capabilities: r.Capabilities, Consent: r.RequireConsent})
	public := r
	public.ControllerAuth = ""
	return Authorization{Session: public, Ticket: base64.RawURLEncoding.EncodeToString(wire), SiteKey: m.PublicKey(), HostKey: st.Hosts[r.HostID].TransportKey, ControllerKey: st.Devices[r.ControllerID].PublicKey, Endpoint: net.JoinHostPort(st.Devices[r.HostID].IP, strconv.Itoa(int(st.Hosts[r.HostID].Port))), ServerUnixMS: m.now().UnixMilli()}, e
}

func (m *Manager) Consume(c model.Credential, id string, sequence uint64, boot string, proof Possession) (Authorization, error) {
	var out Authorization
	e := m.Store.RemoteTargetUpdate(c, id, "", func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		r, ok := st.RemoteSessions[id]
		if !ok || r.HostID != c.DeviceID || r.HostBoot != boot || sequence != r.Sequence {
			return model.ErrForbidden
		}
		if r.State != "issued" {
			return ErrReplay
		}
		if !m.valid(st, r, now) {
			return model.ErrForbidden
		}
		a, e := m.authorization(st, r)
		if e != nil {
			return e
		}
		wire, _ := base64.RawURLEncoding.DecodeString(a.Ticket)
		if !verifyProof(a.ControllerKey, wire, proof) {
			return ErrProof
		}
		r.State = "active"
		st.RemoteSessions[id] = r
		st.Record(c.UserID, "remote.session.accept", id)
		out, e = m.authorization(st, r)
		return e
	})
	return out, e
}

// Inspect returns only current authorization to one of the participating devices.
// Hosts use it to reconstruct bindings from the authenticated control plane.
func (m *Manager) Inspect(c model.Credential, id string) (Authorization, error) {
	var out Authorization
	st, err := m.Store.RemoteTargetView(c, id, "")
	if err != nil {
		return out, err
	}
	err = func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		r, ok := st.RemoteSessions[id]
		if !ok || r.HostID != c.DeviceID && r.ControllerID != c.DeviceID || !m.valid(st, r, now) {
			return model.ErrForbidden
		}
		var e error
		out, e = m.authorization(st, r)
		return e
	}(&st)
	return out, err
}

func (m *Manager) Renew(ctx context.Context, c model.Credential, id string, sequence uint64) (Authorization, error) {
	var out Authorization
	st, e := m.Store.RemoteTargetView(c, id, "")
	if e != nil {
		return out, e
	}
	r, ok := st.RemoteSessions[id]
	if !ok || r.ControllerID != c.DeviceID {
		return out, model.ErrForbidden
	}
	peers, e := m.verifyPeers(ctx, c, r.HostID)
	if e != nil {
		return out, e
	}
	e = m.Store.RemoteTargetUpdate(c, id, "", func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		r, ok := st.RemoteSessions[id]
		if !ok || r.ControllerID != c.DeviceID || r.UserID != c.UserID || r.State != "active" || !m.valid(st, r, now) || !checkPeer(st, c.DeviceID, peers) || !checkPeer(st, r.HostID, peers) {
			return model.ErrForbidden
		}
		if r.Sequence != sequence || sequence == math.MaxUint64 {
			return model.ErrConflict
		}
		r.Sequence++
		r.IssuedAt = now
		r.ExpiresAt = m.expiry(st, c, st.Leases[r.ControllerLease], st.Leases[r.HostLease], st.RemoteGrants[r.GrantID], now)
		st.RemoteSessions[id] = r
		var e error
		out, e = m.authorization(st, r)
		return e
	})
	return out, e
}

func (m *Manager) Revoke(c model.Credential, id string) error {
	return m.Store.RemoteTargetUpdate(c, id, "", func(st *model.State) error {
		if e := current(st, c, m.now()); e != nil {
			return e
		}
		r, ok := st.RemoteSessions[id]
		if !ok {
			return nil
		}
		if r.ControllerID != c.DeviceID && r.HostID != c.DeviceID && st.Users[c.UserID].Role != "admin" {
			return model.ErrForbidden
		}
		r.Close(m.now())
		st.RemoteSessions[id] = r
		st.Record(c.UserID, "remote.session.revoke", id)
		return nil
	})
}
