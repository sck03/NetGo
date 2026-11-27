package session

import (
	"context"
	"netgo/internal/model"
	"testing"
)

func TestResourceMetadataAndNoopRetainLease(t *testing.T) {
	m, g, c, _ := fixture(t)
	lease, err := m.Acquire(context.Background(), c, "p1", 1, false, model.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"same", "rename", "ports"} {
		err = m.MutateResource(context.Background(), "nas", func(st *model.State) error {
			r := st.Resources["nas"]
			if change == "rename" {
				r.Name = "Renamed"
			}
			if change == "ports" {
				r.Ports = append(r.Ports, r.Ports...)
			}
			st.Resources[r.ID] = r
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.Store.Lease(lease.ID); err != nil || len(g.leases) != 1 {
			t.Fatal(change, "revoked lease", err)
		}
	}
	if err = m.MutateResource(context.Background(), "nas", func(st *model.State) error {
		r := st.Resources["nas"]
		r.Enabled = false
		st.Resources[r.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Store.Lease(lease.ID); err == nil || len(g.leases) != 0 {
		t.Fatal("authority change retained lease", err)
	}
}
