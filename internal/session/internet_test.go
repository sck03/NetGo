package session

import (
	"context"
	"netgo/internal/model"
	"testing"
)

func TestOnlyExplicitlyGrantedAccountsReceiveInternetLeases(t *testing.T) {
	m, g, c, _ := fixture(t)
	ctx := context.Background()
	internet := model.Resource{ID: "internet", Name: "Internet via bypass", CIDR: "0.0.0.0/0", Protocol: "internet", Enabled: true}
	if err := m.Store.Update(func(st *model.State) error { st.Resources[internet.ID] = internet; return nil }); err != nil {
		t.Fatal(err)
	}
	lease, err := m.Acquire(ctx, c, "p1", 1, false, "not-granted")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range lease.Resources {
		if r.Protocol == "internet" {
			t.Fatal("available resource became an implicit account permission")
		}
	}
	if err = m.MutateUser(ctx, c.UserID, func(st *model.State) error {
		u := st.Users[c.UserID]
		u.Resources = append(u.Resources, internet.ID)
		st.Users[c.UserID] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lease, err = m.Acquire(ctx, c, "p1", 2, false, "granted")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range lease.Resources {
		found = found || r.Protocol == "internet"
	}
	if !found {
		t.Fatal("explicit account grant missing")
	}
	if err = m.MutateUser(ctx, c.UserID, func(st *model.State) error {
		u := st.Users[c.UserID]
		u.Resources = []string{"nas"}
		st.Users[c.UserID] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(g.leases) != 0 {
		t.Fatal("revoked account retained an active Internet gate")
	}
	if _, err = m.Renew(ctx, c, lease.ID, lease.Generation); err == nil {
		t.Fatal("revoked Internet lease renewed")
	}
	lease, err = m.Acquire(ctx, c, "p1", 3, false, "after-revocation")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range lease.Resources {
		if r.Protocol == "internet" {
			t.Fatal("revoked account reacquired Internet permission")
		}
	}
}
