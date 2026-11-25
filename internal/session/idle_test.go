package session

import (
	"context"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestIdleSweepsSkipGateButStillExpireLeases(t *testing.T) {
	m, g, c, _ := fixture(t)
	lease, err := m.Acquire(context.Background(), c, "p1", 1, false, model.ID())
	if err != nil {
		t.Fatal(err)
	}
	calls := g.calls
	for range 5 {
		if err = m.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if g.calls != calls {
		t.Fatal("unchanged sweeps applied firewall", g.calls)
	}
	m.Now = func() time.Time { return lease.ExpiresAt.Add(time.Second) }
	if err = m.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 0 || g.calls != calls+1 {
		t.Fatal("expiry not applied")
	}
}

func TestOverlayAdmissionWithoutLANResources(t *testing.T) {
	m, _, c, _ := fixture(t)
	if err := m.Store.Update(func(st *model.State) error {
		u := st.Users[c.UserID]
		u.Resources = nil
		st.Users[u.ID] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(context.Background(), c, "p1", 1, false, model.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Resources) != 0 {
		t.Fatal("overlay admission granted LAN access")
	}
	if _, err = m.Renew(context.Background(), c, l.ID, l.Generation); err != nil {
		t.Fatal(err)
	}
}
