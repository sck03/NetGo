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
