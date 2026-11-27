package session

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
)

type renewalGate struct {
	fakeGate
	renewed int
	failure error
}

func (g *renewalGate) Renew(_ context.Context, lease model.Lease) (bool, error) {
	g.renewed++
	if g.failure != nil {
		return false, g.failure
	}
	g.leases = []model.Lease{lease}
	return true, nil
}
func TestRenewalDeltaAndFailureFence(t *testing.T) {
	m, _, credential, _ := fixture(t)
	g := &renewalGate{}
	m.Gate = g
	ctx := context.Background()
	lease, err := m.Acquire(ctx, credential, "p1", 1, false, "acquire")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Renew(ctx, credential, lease.ID, 1); err != nil {
		t.Fatal(err)
	}
	if g.calls != 1 || g.renewed != 1 {
		t.Fatal("unchanged renewal rebuilt policy", g.calls, g.renewed)
	}
	g.failure = errors.New("helper unavailable")
	if _, err = m.Renew(ctx, credential, lease.ID, 1); err == nil {
		t.Fatal("failed delta accepted")
	}
	state, err := m.Store.View()
	if err != nil || len(state.Leases) != 0 || len(g.leases) != 0 {
		t.Fatal("failure did not close access", err)
	}
}
