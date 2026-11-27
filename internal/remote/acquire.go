package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"netgo/internal/model"
	"netgo/internal/remote/ticket"
	"netgo/internal/session"
	"netgo/internal/store"
	"time"
)

type Request struct {
	HostID       string `json:"host_id"`
	Generation   uint64 `json:"generation"`
	Capabilities uint32 `json:"capabilities"`
}
type Authorization struct {
	Session       model.RemoteSession `json:"session"`
	Ticket        string              `json:"ticket"`
	SiteKey       string              `json:"site_key"`
	HostKey       string              `json:"host_key"`
	ControllerKey string              `json:"controller_key"`
	Endpoint      string              `json:"endpoint"`
	ServerUnixMS  int64               `json:"server_unix_ms"`
}

// verifyPeers runs outside SQLite transactions; the transaction rechecks all
// identity/lease values against the authenticated result before issuing a grant.
func (m *Manager) verifyPeers(ctx context.Context, c model.Credential, host string) (map[string]session.Binding, error) {
	if m.Peers == nil {
		return nil, ErrUnavailable
	}
	st, e := m.Store.RemoteTargetView(c, "", host)
	if e != nil {
		return nil, e
	}
	if e = current(&st, c, m.now()); e != nil {
		return nil, e
	}
	out := map[string]session.Binding{}
	for _, id := range []string{c.DeviceID, host} {
		d, ok := st.Devices[id]
		if !ok || d.PeerID == "" {
			return nil, ErrUnavailable
		}
		p, e := m.Peers.Verify(ctx, d.PeerID)
		if e != nil {
			return nil, model.ControlFailure(e)
		}
		if !p.Connected || p.Expired || p.UserID != d.UserID || p.PublicKey != d.WireGuardKey || p.IP != d.IP || net.ParseIP(p.IP) == nil {
			return nil, ErrUnavailable
		}
		out[id] = p
	}
	return out, nil
}

func checkPeer(st *model.State, id string, peers map[string]session.Binding) bool {
	d := st.Devices[id]
	p, ok := peers[id]
	return ok && d.UserID == p.UserID && d.WireGuardKey == p.PublicKey && d.PeerID == p.ID && d.IP == p.IP
}

func (m *Manager) Acquire(ctx context.Context, c model.Credential, req Request, key string) (Authorization, error) {
	var out Authorization
	if req.Generation == 0 || req.Generation > math.MaxInt64 || !capabilities(req.Capabilities) || len(key) < 16 || len(key) > 128 || req.HostID == c.DeviceID {
		return out, errors.New("invalid_remote_request")
	}
	peers, e := m.verifyPeers(ctx, c, req.HostID)
	if e != nil {
		return out, e
	}
	fingerprint := model.Hash(fmt.Sprintf("%s|%s|%s|%d|%d", c.UserID, c.DeviceID, req.HostID, req.Generation, req.Capabilities))
	opKey := model.Hash(c.UserID + ":" + c.DeviceID + ":" + key)
	e = m.Store.RemoteAcquireUpdate(c, req.HostID, opKey, m.now(), func(st *model.State, limits store.RemoteAcquireLimits) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		m.sweep(st, now)
		if !checkPeer(st, c.DeviceID, peers) || !checkPeer(st, req.HostID, peers) {
			return model.ErrForbidden
		}
		if op, ok := st.RemoteOperations[opKey]; ok && now.Before(op.ExpiresAt) {
			r, exists := st.RemoteSessions[op.SessionID]
			if op.Fingerprint != fingerprint || !exists || !m.valid(st, r, now) {
				return model.ErrConflict
			}
			var e error
			out, e = m.authorization(st, r)
			return e
		}
		h, ok := st.Hosts[req.HostID]
		if !ok || hostStatus(st, h, now) != "ready" {
			return ErrUnavailable
		}
		g, ok := st.RemoteGrants[model.Hash(c.UserID+":"+h.ID)]
		if !ok || (!g.ExpiresAt.IsZero() && !now.Before(g.ExpiresAt)) || req.Capabilities & ^(g.Capabilities&h.Capabilities) != 0 {
			return model.ErrForbidden
		}
		cl, cok := activeLease(st, c.DeviceID, now)
		hl, hok := activeLease(st, h.ID, now)
		if !cok || !hok || st.Devices[c.DeviceID].ClientType == "admin" {
			return model.ErrForbidden
		}
		active := 0
		if req.Generation <= limits.Generation {
			return model.ErrConflict
		}
		for _, r := range st.RemoteSessions {
			if r.State != "closed" {
				if r.HostID == h.ID {
					return ErrBusy
				}
				if r.UserID == c.UserID {
					active++
				}
			}
		}
		if active >= st.Users[c.UserID].MaxSessions || limits.Retained >= 65536 {
			return model.ErrConflict
		}
		hostKey, e := decode(h.TransportKey, 32)
		if e != nil {
			return e
		}
		controllerKey, e := decode(st.Devices[c.DeviceID].PublicKey, 32)
		if e != nil {
			return e
		}
		boot, e := decode(h.Boot, 16)
		if e != nil {
			return e
		}
		binding, e := (ticket.Binding{SiteID: m.SiteID, UserID: c.UserID, ControllerID: c.DeviceID, HostID: h.ID, SiteKey: [32]byte(m.Key.Public().(ed25519.PublicKey)), ControllerKey: [32]byte(controllerKey), HostKey: [32]byte(hostKey), HostBoot: [16]byte(boot)}).Digest()
		if e != nil {
			return e
		}
		var id [16]byte
		if _, e = rand.Read(id[:]); e != nil {
			return e
		}
		r := model.RemoteSession{ID: base64.RawURLEncoding.EncodeToString(id[:]), UserID: c.UserID, ControllerID: c.DeviceID, HostID: h.ID, HostBoot: h.Boot, GrantID: g.ID, Binding: base64.RawURLEncoding.EncodeToString(binding[:]), Generation: req.Generation, PolicyRevision: g.Revision, Sequence: 1, Capabilities: req.Capabilities, RequireConsent: h.RequireConsent, CreatedAt: now.Truncate(time.Millisecond), IssuedAt: now, ExpiresAt: m.expiry(st, c, cl, hl, g, now), State: "issued", ControllerLease: cl.ID, HostLease: hl.ID, ControllerAuth: c.TokenHash}
		if !r.ExpiresAt.After(now) {
			return model.ErrForbidden
		}
		st.RemoteSessions[r.ID] = r
		st.RemoteOperations[opKey] = model.RemoteOperation{Fingerprint: fingerprint, SessionID: r.ID, ExpiresAt: now.Add(10 * time.Minute)}
		st.Record(c.UserID, "remote.session.issue", r.ID)
		out, e = m.authorization(st, r)
		return e
	})
	return out, e
}
