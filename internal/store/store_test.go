package store

import (
	"errors"
	"netgo/internal/model"
	"path/filepath"
	"testing"
)

func TestRollbackAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	e = s.Update(func(st *model.State) error { st.Users["u"] = model.User{ID: "u"}; return nil })
	if e != nil {
		t.Fatal(e)
	}
	_ = s.Update(func(st *model.State) error { delete(st.Users, "u"); return errors.New("rollback") })
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st, e := s.View()
	if e != nil || st.Users["u"].ID != "u" {
		t.Fatal("atomic rollback/reopen failed", e)
	}
}
func TestOnlyPolicyChangesInvalidateObservedRevision(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Update(func(st *model.State) error { st.Users["u"] = model.User{ID: "u"}; return nil }); err != nil {
		t.Fatal(err)
	}
	before, _ := s.View()
	if err = s.Update(func(st *model.State) error {
		st.ObservedRevision = st.Revision
		st.Record("u", "session.renew", "l")
		st.Devices["d"] = model.Device{ID: "d", UserID: "u"}
		st.Leases["l"] = model.Lease{ID: "l", UserID: "u", DeviceID: "d"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.View()
	if after.Revision != before.Revision || after.ObservedRevision != after.Revision {
		t.Fatal("non-policy update caused permanent reconciliation drift", after)
	}
	_ = s.Update(func(st *model.State) error { u := st.Users["u"]; u.Disabled = true; st.Users["u"] = u; return nil })
	changed, _ := s.View()
	if changed.Revision <= changed.ObservedRevision {
		t.Fatal("policy change did not invalidate observed revision")
	}
}
