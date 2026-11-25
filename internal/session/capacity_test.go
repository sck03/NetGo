package session

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/gateway"
	"netgo/internal/model"
	"testing"
)

func TestCapacityRejectionPreservesInstalledLeases(t *testing.T) {
	m, gate, c, _ := fixture(t)
	lease, e := m.Acquire(context.Background(), c, "p1", 1, false, "first")
	if e != nil {
		t.Fatal(e)
	}
	calls := gate.calls
	e = m.update(context.Background(), func(st *model.State) error {
		for i := 0; i < gateway.MaxLeases; i++ {
			copy := lease
			copy.ID = fmt.Sprint(i)
			st.Leases[copy.ID] = copy
		}
		return nil
	})
	if !errors.Is(e, gateway.ErrCapacity) || gate.calls != calls {
		t.Fatal("capacity touched gate", e, gate.calls, calls)
	}
	st, _ := m.Store.View()
	if len(st.Leases) != 1 || st.Leases[lease.ID].ID != lease.ID || len(gate.leases) != 1 {
		t.Fatal("existing access was revoked")
	}
	e = m.update(context.Background(), func(st *model.State) error {
		l := st.Leases[lease.ID]
		l.Resources = make([]model.Resource, 10000)
		for i := range l.Resources {
			l.Resources[i] = model.Resource{ID: fmt.Sprint(i), Name: string(make([]byte, 256))}
		}
		st.Leases[l.ID] = l
		return nil
	})
	if !errors.Is(e, gateway.ErrCapacity) || gate.calls != calls {
		t.Fatal("payload limit touched gate", e)
	}
}
func TestResourceDeletionCleansGrantsAndRevokesLease(t *testing.T) {
	m, gate, c, _ := fixture(t)
	if _, e := m.Acquire(context.Background(), c, "p1", 1, false, "first"); e != nil {
		t.Fatal(e)
	}
	if e := m.MutateResource(context.Background(), "nas", func(st *model.State) error { delete(st.Resources, "nas"); return nil }); e != nil {
		t.Fatal(e)
	}
	st, e := m.Store.View()
	if e != nil || len(st.Users["u"].Resources) != 0 || len(st.Leases) != 0 || len(gate.leases) != 0 {
		t.Fatal("dangling resource authorization", st, e)
	}
}
