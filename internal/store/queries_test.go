package store

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestAuditCursorIsolationAndRollback(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Update(func(st *model.State) error {
		for i := 0; i < 125; i++ {
			st.Record("admin", "test", model.ID())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	_ = s.Update(func(st *model.State) error {
		if len(st.Audit) != 0 {
			t.Fatal("history loaded into transaction")
		}
		st.Record("admin", "rollback", "")
		return errors.New("rollback")
	})
	first, e := s.Audit(context.Background(), 0, 100)
	if e != nil || len(first.Events) != 100 || first.NextBefore == 0 {
		t.Fatal(first, e)
	}
	_ = s.Update(func(st *model.State) error { st.Record("admin", "new", ""); return nil })
	second, e := s.Audit(context.Background(), first.NextBefore, 100)
	if e != nil || len(second.Events) != 25 || second.NextBefore != 0 {
		t.Fatal(second, e)
	}
	for _, item := range second.Events {
		if item.Sequence >= first.NextBefore {
			t.Fatal("overlapping cursor")
		}
	}
}
func TestScopedUpdatePreservesUnloadedRowsAndIdentity(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	e = s.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Alias: "Alice", Resources: []string{"r"}}
		st.Resources["r"] = model.Resource{ID: "r", Ports: []uint16{443}}
		st.Devices["d"] = model.Device{ID: "d", UserID: "u"}
		st.Credentials["c"] = model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
		st.Operations["o"] = model.Operation{Fingerprint: "kept"}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	e = s.RemoteHostUpdate(context.Background(), model.Credential{UserID: "u", DeviceID: "d", TokenHash: "c"}, "d", func(st *model.State) error {
		if len(st.Resources) != 0 || len(st.Operations) != 0 {
			t.Fatal("unrelated table loaded")
		}
		st.Record("u", "remote.test", "")
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	st, e := s.View()
	if e != nil || st.Resources["r"].Ports[0] != 443 || st.Operations["o"].Fingerprint != "kept" || len(st.Users["u"].Resources) != 1 {
		t.Fatal(st, e)
	}
	c, u, d, e := s.Identity(context.Background(), "c")
	if e != nil || c.TokenHash != "c" || u.ID != "u" || d.ID != "d" {
		t.Fatal(c, u, d, e)
	}
	u, e = s.UserByAlias("ALICE")
	if e != nil || u.ID != "u" {
		t.Fatal(u, e)
	}
}
func TestRollbackCannotMutateNestedCollections(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_ = s.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Resources: []string{"r"}}
		st.Resources["r"] = model.Resource{ID: "r", Ports: []uint16{443}}
		return nil
	})
	_ = s.Update(func(st *model.State) error {
		u := st.Users["u"]
		u.Resources[0] = "bad"
		r := st.Resources["r"]
		r.Ports[0] = 1
		return errors.New("rollback")
	})
	st, _ := s.View()
	if st.Users["u"].Resources[0] != "r" || st.Resources["r"].Ports[0] != 443 {
		t.Fatal("aliased rollback state")
	}
}
