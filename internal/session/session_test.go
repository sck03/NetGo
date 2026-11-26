package session

import (
	"context"
	"errors"
	"netgo/internal/model"
	"netgo/internal/store"
	"sync"
	"testing"
	"time"
)

type fakeGate struct {
	leases []model.Lease
	fail   bool
	calls  int
}

func (g *fakeGate) Apply(_ context.Context, l []model.Lease) error {
	g.calls++
	if g.fail {
		return errors.New("nft failed")
	}
	g.leases = append([]model.Lease(nil), l...)
	return nil
}

type fakePeers map[string]Binding

func (p fakePeers) Verify(_ context.Context, id string) (Binding, error) {
	v, ok := p[id]
	if !ok {
		return v, model.ErrForbidden
	}
	return v, nil
}
func fixture(t *testing.T) (*Manager, *fakeGate, model.Credential, model.Credential) {
	t.Helper()
	s, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now().UTC()
	g := &fakeGate{}
	m := &Manager{Store: s, Gate: g, TTL: 120 * time.Second, Now: func() time.Time { return now }, Peers: fakePeers{"p1": {ID: "p1", UserID: "u", IP: "100.96.0.2", PublicKey: "wg1", Connected: true}, "p2": {ID: "p2", UserID: "u", IP: "100.96.0.3", PublicKey: "wg2", Connected: true}}}
	e = s.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", MaxDevices: 3, MaxSessions: 1, Resources: []string{"nas"}}
		st.Resources["nas"] = model.Resource{ID: "nas", Name: "NAS", CIDR: "192.168.50.20/32", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}
		st.Devices["d1"] = model.Device{ID: "d1", UserID: "u", WireGuardKey: "wg1"}
		st.Devices["d2"] = model.Device{ID: "d2", UserID: "u", WireGuardKey: "wg2"}
		for _, id := range []string{"d1", "d2"} {
			st.Credentials[model.Hash(id)] = model.Credential{UserID: "u", DeviceID: id, ExpiresAt: now.Add(time.Hour)}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return m, g, model.Credential{TokenHash: model.Hash("d1"), UserID: "u", DeviceID: "d1", ExpiresAt: now.Add(time.Hour)}, model.Credential{TokenHash: model.Hash("d2"), UserID: "u", DeviceID: "d2", ExpiresAt: now.Add(time.Hour)}
}
func TestConcurrentSingleLease(t *testing.T) {
	m, g, c, _ := fixture(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := m.Acquire(context.Background(), c, "p1", 1, false, model.ID())
			if e == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(e, model.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || len(g.leases) != 1 {
		t.Fatalf("wins=%d leases=%d", wins, len(g.leases))
	}
}

func TestConfiguredConcurrentLimitAndExplicitReplacement(t *testing.T) {
	m, g, c1, c2 := fixture(t)
	if err := m.Store.Update(func(st *model.State) error { u := st.Users["u"]; u.MaxSessions = 2; st.Users["u"] = u; return nil }); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := m.Acquire(ctx, c1, "p1", 1, false, "device-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire(ctx, c2, "p2", 1, false, "device-two"); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 2 {
		t.Fatal("configured second session was not admitted")
	}
	if _, err := m.Acquire(ctx, c1, "p1", 1, false, "duplicate-device"); !errors.Is(err, model.ErrConflict) {
		t.Fatal("duplicate device admitted", err)
	}
	if err := m.Revoke(ctx, c1.UserID, g.leases[0].ID); err != nil {
		t.Fatal(err)
	}
	st, err := m.Store.View()
	if err != nil {
		t.Fatal(err)
	}
	// Reconnect whichever device was revoked, explicitly replacing the other.
	credential, peer := c1, "p1"
	for _, l := range st.Leases {
		if l.DeviceID == c1.DeviceID {
			credential, peer = c2, "p2"
		}
	}
	if _, err := m.Acquire(ctx, credential, peer, 3, true, "replace-all"); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 1 {
		t.Fatal("explicit replacement retained another session")
	}
}

func TestAuthenticatedDeviceRestartRejectsOldGeneration(t *testing.T) {
	m, g, credential, _ := fixture(t)
	ctx := context.Background()
	old, err := m.Acquire(ctx, credential, "p1", 10, false, "old")
	if err != nil {
		t.Fatal(err)
	}
	next, err := m.Acquire(ctx, credential, "p1", 11, false, "restart")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 1 || next.ID == old.ID {
		t.Fatal("same-device restart retained old lease")
	}
	before := g.calls
	if _, err := m.Acquire(ctx, credential, "p1", 10, false, "late-request"); !errors.Is(err, model.ErrConflict) {
		t.Fatal("stale generation replaced new session", err)
	}
	if g.calls != before {
		t.Fatal("rejected restart changed gate")
	}
	if _, err := m.Renew(ctx, credential, old.ID, 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatal("old lease renewed")
	}
}

func TestManagementCredentialCannotAcquireNetworkLease(t *testing.T) {
	m, _, credential, _ := fixture(t)
	_ = m.Store.Update(func(st *model.State) error {
		device := st.Devices["d1"]
		device.ClientType = "admin"
		st.Devices["d1"] = device
		return nil
	})
	if _, err := m.Acquire(context.Background(), credential, "p1", 1, false, "management-key"); !errors.Is(err, model.ErrForbidden) {
		t.Fatal("management key obtained network access", err)
	}
}

func TestScopedMutationPreservesOtherUsers(t *testing.T) {
	m, g, c1, _ := fixture(t)
	peers := m.Peers.(fakePeers)
	peers["p3"] = Binding{ID: "p3", UserID: "v", IP: "100.96.0.4", PublicKey: "wg3", Connected: true}
	if err := m.Store.Update(func(st *model.State) error {
		st.Users["v"] = model.User{ID: "v", MaxDevices: 1, MaxSessions: 1, Resources: []string{"ssh"}}
		st.Resources["ssh"] = model.Resource{ID: "ssh", CIDR: "192.168.50.21/32", Protocol: "tcp", Ports: []uint16{22}, Enabled: true}
		st.Devices["d3"] = model.Device{ID: "d3", UserID: "v", WireGuardKey: "wg3"}
		st.Credentials[model.Hash("d3")] = model.Credential{UserID: "v", DeviceID: "d3", ExpiresAt: c1.ExpiresAt}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c3 := model.Credential{TokenHash: model.Hash("d3"), UserID: "v", DeviceID: "d3", ExpiresAt: c1.ExpiresAt}
	ctx := context.Background()
	if _, err := m.Acquire(ctx, c1, "p1", 1, false, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire(ctx, c3, "p3", 1, false, "other"); err != nil {
		t.Fatal(err)
	}
	if err := m.MutateUser(ctx, "u", func(st *model.State) error { u := st.Users["u"]; u.Disabled = true; st.Users["u"] = u; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 1 || g.leases[0].UserID != "v" {
		t.Fatal("unrelated user disconnected")
	}
	if err := m.MutateResource(ctx, "nas", func(st *model.State) error { delete(st.Resources, "nas"); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 1 {
		t.Fatal("unrelated resource mutation revoked lease")
	}
	if err := m.MutateDevice(ctx, "d3", func(st *model.State) error { delete(st.Devices, "d3"); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 0 {
		t.Fatal("device revocation retained access")
	}
}
func TestReplacementIdempotencyAndStaleRenew(t *testing.T) {
	m, g, c1, c2 := fixture(t)
	ctx := context.Background()
	old, e := m.Acquire(ctx, c1, "p1", 1, false, "first")
	if e != nil {
		t.Fatal(e)
	}
	next, e := m.Acquire(ctx, c2, "p2", 2, true, "replace")
	if e != nil {
		t.Fatal(e)
	}
	retry, e := m.Acquire(ctx, c2, "p2", 2, true, "replace")
	if e != nil || retry.ID != next.ID {
		t.Fatal("non-idempotent retry", e)
	}
	before := g.calls
	if _, e = m.Renew(ctx, c1, old.ID, 1); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("stale renew accepted")
	}
	if g.calls != before || len(g.leases) != 1 || g.leases[0].ID != next.ID {
		t.Fatal("stale renew disturbed current session")
	}
}
func TestForgedPeerAndConflictingKeyDoNotRevoke(t *testing.T) {
	m, g, c1, _ := fixture(t)
	ctx := context.Background()
	_, e := m.Acquire(ctx, c1, "p1", 1, false, "key")
	if e != nil {
		t.Fatal(e)
	}
	calls := g.calls
	_, e = m.Acquire(ctx, c1, "p2", 2, true, "other")
	if !errors.Is(e, model.ErrForbidden) || g.calls != calls {
		t.Fatal("forged binding reached gate")
	}
	_, e = m.Acquire(ctx, c1, "p1", 2, true, "key")
	if !errors.Is(e, model.ErrConflict) || g.calls != calls {
		t.Fatal("idempotency collision reached gate")
	}
}
func TestExpiryAndRestartFailClosed(t *testing.T) {
	m, g, c, _ := fixture(t)
	_, e := m.Acquire(context.Background(), c, "p1", 1, false, "key")
	if e != nil {
		t.Fatal(e)
	}
	now := m.now().Add(121 * time.Second)
	m.Now = func() time.Time { return now }
	if e = m.Sweep(context.Background()); e != nil || len(g.leases) != 0 {
		t.Fatal("expired lease retained", e)
	}
	if e = m.Reset(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestGateFailureNeverPersistsGrant(t *testing.T) {
	m, g, c, _ := fixture(t)
	g.fail = true
	if _, e := m.Acquire(context.Background(), c, "p1", 1, false, "key"); e == nil {
		t.Fatal("gate failure ignored")
	}
	st, _ := m.Store.View()
	if len(st.Leases) != 0 {
		t.Fatal("failed lease persisted")
	}
}
func TestAccountExpiryCapsLease(t *testing.T) {
	m, _, c, _ := fixture(t)
	expiry := m.now().Add(20 * time.Second)
	_ = m.Store.Update(func(s *model.State) error { u := s.Users["u"]; u.ExpiresAt = expiry; s.Users["u"] = u; return nil })
	l, e := m.Acquire(context.Background(), c, "p1", 1, false, "k")
	if e != nil || !l.ExpiresAt.Equal(expiry) {
		t.Fatal("account expiry not enforced", e)
	}
}
