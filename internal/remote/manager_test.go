package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math"
	"netgo/internal/model"
	"netgo/internal/remote/ticket"
	"netgo/internal/session"
	"netgo/internal/store"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type peerSet map[string]session.Binding

func (p peerSet) Verify(_ context.Context, id string) (session.Binding, error) {
	v, ok := p[id]
	if !ok {
		return v, model.ErrNotFound
	}
	return v, nil
}

type fixture struct {
	m          *Manager
	c, h       model.Credential
	controller ed25519.PrivateKey
	now        time.Time
	path, boot string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Now().UTC().Truncate(time.Millisecond), path: filepath.Join(t.TempDir(), "state.db"), boot: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))}
	db, e := store.Open(f.path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.m.Store.Close() })
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	f.controller = key
	hp, _, _ := ed25519.GenerateKey(rand.Reader)
	_, sign, _ := ed25519.GenerateKey(rand.Reader)
	f.c = model.Credential{TokenHash: model.Hash("controller"), UserID: "user", DeviceID: "controller", ExpiresAt: f.now.Add(time.Hour)}
	f.h = model.Credential{TokenHash: model.Hash("host"), UserID: "owner", DeviceID: "host", ExpiresAt: f.now.Add(time.Hour)}
	peers := peerSet{"pc": {ID: "pc", UserID: "user", IP: "100.96.0.2", PublicKey: "wc", Connected: true}, "ph": {ID: "ph", UserID: "owner", IP: "100.96.0.3", PublicKey: "wh", Connected: true}}
	f.m = &Manager{Store: db, SiteID: "site", Key: sign, Peers: peers, Now: func() time.Time { return f.now }}
	e = db.Update(func(st *model.State) error {
		st.Users["user"] = model.User{ID: "user", Role: "admin", MaxDevices: 3, MaxSessions: 3}
		st.Users["owner"] = model.User{ID: "owner", Role: "user", MaxDevices: 3, MaxSessions: 3}
		st.Devices["controller"] = model.Device{ID: "controller", UserID: "user", PublicKey: base64.RawURLEncoding.EncodeToString(pub), WireGuardKey: "wc", PeerID: "pc", IP: "100.96.0.2"}
		st.Devices["host"] = model.Device{ID: "host", UserID: "owner", PublicKey: base64.RawURLEncoding.EncodeToString(hp), WireGuardKey: "wh", PeerID: "ph", IP: "100.96.0.3"}
		st.Credentials[f.c.TokenHash] = f.c
		st.Credentials[f.h.TokenHash] = f.h
		st.Leases["lc"] = model.Lease{ID: "lc", UserID: "user", DeviceID: "controller", PeerID: "pc", IP: "100.96.0.2", Generation: 1, ExpiresAt: f.now.Add(time.Minute)}
		st.Leases["lh"] = model.Lease{ID: "lh", UserID: "owner", DeviceID: "host", PeerID: "ph", IP: "100.96.0.3", Generation: 1, ExpiresAt: f.now.Add(time.Minute)}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.Register(f.h, model.Host{ID: "host", Name: "Workstation", Platform: "windows", Boot: f.boot, Port: 29990, Enabled: true, Capabilities: ticket.CapabilityView | ticket.CapabilityInput, RequireConsent: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.Grant(f.c, model.RemoteGrant{HostID: "host", UserID: "user", Capabilities: ticket.CapabilityView | ticket.CapabilityInput}); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) acquire(t *testing.T) Authorization {
	t.Helper()
	a, e := f.m.Acquire(context.Background(), f.c, Request{HostID: "host", Generation: 1, Capabilities: ticket.CapabilityView}, "operation-key-012345")
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func proof(t *testing.T, a Authorization, key ed25519.PrivateKey) Possession {
	t.Helper()
	wire, e := base64.RawURLEncoding.DecodeString(a.Ticket)
	if e != nil {
		t.Fatal(e)
	}
	ch := bytes.Repeat([]byte{2}, 32)
	channel := bytes.Repeat([]byte{3}, 32)
	msg, e := ProofMessage(wire, ch, channel)
	if e != nil {
		t.Fatal(e)
	}
	return Possession{Challenge: base64.RawURLEncoding.EncodeToString(ch), Channel: base64.RawURLEncoding.EncodeToString(channel), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, msg))}
}
func TestLifecycleAndSignedBindings(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if a.Endpoint != "100.96.0.3:29990" || a.Session.ControllerAuth != "" {
		t.Fatal("unexpected public authorization", a.Session)
	}
	if a.ServerUnixMS != f.now.UnixMilli() {
		t.Fatal("missing authenticated server clock")
	}
	wire, _ := base64.RawURLEncoding.DecodeString(a.Ticket)
	claims, e := ticket.Verify(f.m.Key.Public().(ed25519.PublicKey), wire)
	if e != nil || claims.Capabilities != ticket.CapabilityView || !claims.Consent || claims.ExpiresMS-claims.IssuedMS != 30_000 {
		t.Fatal(claims, e)
	}
	duplicate := f.acquire(t)
	if duplicate.Ticket != a.Ticket {
		t.Fatal("idempotency changed ticket")
	}
	if _, e = f.m.Acquire(context.Background(), f.c, Request{HostID: "host", Generation: 2, Capabilities: 1}, "operation-key-012345"); !errors.Is(e, model.ErrConflict) {
		t.Fatal("key reused for another operation", e)
	}
	if _, e = f.m.Consume(f.c, a.Session.ID, 1, f.boot, proof(t, a, f.controller)); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("controller impersonated host", e)
	}
	if _, e = f.m.Consume(f.h, a.Session.ID, 1, f.boot, proof(t, a, f.controller)); e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(10 * time.Second)
	if e = f.m.Heartbeat(f.h, f.boot); e != nil {
		t.Fatal(e)
	}
	renewed, e := f.m.Renew(context.Background(), f.c, a.Session.ID, 1)
	if e != nil || renewed.Session.Sequence != 2 {
		t.Fatal(e)
	}
	if _, e = f.m.Renew(context.Background(), f.c, a.Session.ID, 1); !errors.Is(e, model.ErrConflict) {
		t.Fatal("old sequence renewed", e)
	}
	if e = f.m.Revoke(f.c, a.Session.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.Renew(context.Background(), f.c, a.Session.ID, 2); e == nil {
		t.Fatal("revoked session resurrected")
	}
}
func TestProofTamperingAndConcurrentPersistentConsumption(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	p := proof(t, a, f.controller)
	for _, mutate := range []func(*Possession){func(p *Possession) { p.Channel = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)) }, func(p *Possession) { p.Challenge = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)) }, func(p *Possession) { p.Signature = "bad" }} {
		bad := p
		mutate(&bad)
		if _, e := f.m.Consume(f.h, a.Session.ID, 1, f.boot, bad); !errors.Is(e, ErrProof) {
			t.Fatal("accepted substituted proof", e)
		}
	}
	second, e := store.Open(f.path)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	m2 := *f.m
	m2.Store = second
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := f.m
			if i%2 == 1 {
				m = &m2
			}
			_, e := m.Consume(f.h, a.Session.ID, 1, f.boot, p)
			if e == nil {
				successes.Add(1)
			} else if !errors.Is(e, ErrReplay) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("multiple ticket consumers", successes.Load())
	}
	f.m.Store.Close()
	f.m.Store, e = store.Open(f.path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.Consume(f.h, a.Session.ID, 1, f.boot, p); !errors.Is(e, ErrReplay) {
		t.Fatal("replay after restart", e)
	}
}
func TestRevocationDependenciesFailClosed(t *testing.T) {
	for name, mutate := range map[string]func(*fixture, *model.State){
		"controller logout":   func(f *fixture, s *model.State) { delete(s.Credentials, f.c.TokenHash) },
		"controller disabled": func(f *fixture, s *model.State) { u := s.Users["user"]; u.Disabled = true; s.Users[u.ID] = u },
		"host disabled":       func(f *fixture, s *model.State) { h := s.Hosts["host"]; h.Enabled = false; s.Hosts[h.ID] = h },
		"host restart": func(f *fixture, s *model.State) {
			h := s.Hosts["host"]
			h.Boot = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 16))
			s.Hosts[h.ID] = h
		},
		"host owner disabled": func(f *fixture, s *model.State) { u := s.Users["owner"]; u.Disabled = true; s.Users[u.ID] = u },
		"vpn replaced":        func(f *fixture, s *model.State) { delete(s.Leases, "lc") },
		"grant removed":       func(f *fixture, s *model.State) { delete(s.RemoteGrants, model.Hash("user:host")) },
		"grant downscoped": func(f *fixture, s *model.State) {
			g := s.RemoteGrants[model.Hash("user:host")]
			g.Revision++
			s.RemoteGrants[g.ID] = g
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			a := f.acquire(t)
			p := proof(t, a, f.controller)
			if e := f.m.Store.Update(func(s *model.State) error { mutate(f, s); return nil }); e != nil {
				t.Fatal(e)
			}
			if _, e := f.m.Consume(f.h, a.Session.ID, 1, f.boot, p); e == nil {
				t.Fatal("revoked dependency accepted")
			}
			if e := f.m.Sweep(); e != nil {
				t.Fatal(e)
			}
			s, _ := f.m.Store.View()
			if s.RemoteSessions[a.Session.ID].State != "closed" {
				t.Fatal("gate did not close")
			}
		})
	}
}
func TestLimitsExpiryAndRegistration(t *testing.T) {
	f := setup(t)
	for _, caps := range []uint32{0, ticket.CapabilityInput, 128, ticket.CapabilityView | ticket.CapabilityAudio} {
		if _, e := f.m.Acquire(context.Background(), f.c, Request{HostID: "host", Generation: 1, Capabilities: caps}, "operation-key-012345"); e == nil {
			t.Fatal("excess capabilities", caps)
		}
	}
	a := f.acquire(t)
	f.now = f.now.Add(16 * time.Second)
	if _, e := f.m.Consume(f.h, a.Session.ID, 1, f.boot, proof(t, a, f.controller)); e == nil {
		t.Fatal("offline host accepted")
	}
	f.now = f.now.Add(-17 * time.Second)
	if _, e := f.m.Consume(f.h, a.Session.ID, 1, f.boot, proof(t, a, f.controller)); e == nil {
		t.Fatal("clock rollback accepted")
	}
}

func TestInspectOnlyCurrentSessionParticipants(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	for _, c := range []model.Credential{f.c, f.h} {
		got, err := f.m.Inspect(c, a.Session.ID)
		if err != nil || got.Ticket != a.Ticket || got.Session.ControllerAuth != "" {
			t.Fatalf("participant inspect: %v", err)
		}
	}
	// Even an administrator on a different device must not retrieve a ticket.
	outsider := model.Credential{TokenHash: model.Hash("outsider"), UserID: f.c.UserID, DeviceID: "outsider", ExpiresAt: f.now.Add(time.Hour)}
	if err := f.m.Store.Update(func(st *model.State) error {
		st.Devices[outsider.DeviceID] = model.Device{ID: outsider.DeviceID, UserID: outsider.UserID}
		st.Credentials[outsider.TokenHash] = outsider
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Inspect(outsider, a.Session.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatal("unrelated device inspected session", err)
	}
	if err := f.m.Revoke(f.c, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Credential{f.c, f.h} {
		if _, err := f.m.Inspect(c, a.Session.ID); !errors.Is(err, model.ErrForbidden) {
			t.Fatal("revoked session inspected", err)
		}
	}
}

func TestNextGenerationSurvivesClosedSessionsAndRestart(t *testing.T) {
	f := setup(t)
	list, err := f.m.List(f.c)
	if err != nil || list.NextGeneration != 1 {
		t.Fatal(list.NextGeneration, err)
	}
	a := f.acquire(t)
	if err := f.m.Revoke(f.c, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	f.m.Store.Close()
	f.m.Store, err = store.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	list, err = f.m.List(f.c)
	if err != nil || list.NextGeneration != 2 {
		t.Fatal(list.NextGeneration, err)
	}
	other, err := f.m.List(f.h)
	if err != nil || other.NextGeneration != 1 {
		t.Fatal("generation leaked across devices", other.NextGeneration, err)
	}
	if err := f.m.Store.Update(func(st *model.State) error {
		r := st.RemoteSessions[a.Session.ID]
		r.Generation = math.MaxInt64
		st.RemoteSessions[r.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	list, err = f.m.List(f.c)
	if err != nil || list.NextGeneration != 0 {
		t.Fatal("generation overflow", list.NextGeneration, err)
	}
}
