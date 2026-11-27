package accounts

import (
	"context"
	"errors"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/store"
	"testing"
)

func TestStaleAccountEditCannotRestoreAuthority(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "rename"}[metadata], func(t *testing.T) {
			db, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			original := model.User{ID: "u", Alias: "user", Name: "User", Email: "u@example.test", Role: "user", MaxDevices: 3, MaxSessions: 1}
			if err = db.Update(func(st *model.State) error { st.Users[original.ID] = original; return nil }); err != nil {
				t.Fatal(err)
			}
			s := Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}}
			change := original
			if metadata {
				change.Name = "New name"
			} else {
				change.Disabled = true
			}
			saved, err := s.Update(context.Background(), change)
			if err != nil {
				t.Fatal(err)
			}
			if saved.EditRevision != 1 || (metadata && saved.AuthRevision != 0) {
				t.Fatalf("wrong revisions: %+v", saved)
			}
			stale := original
			stale.Name = "Stale name"
			if _, err = s.Update(context.Background(), stale); !errors.Is(err, model.ErrEditConflict) {
				t.Fatal("stale edit accepted", err)
			}
			current, err := db.User(original.ID)
			if err != nil || current.Disabled != saved.Disabled || current.Name != saved.Name {
				t.Fatal("stale edit overwrote account", current, err)
			}
			unchanged, err := s.Update(context.Background(), current)
			if err != nil || unchanged.EditRevision != current.EditRevision {
				t.Fatal("no-op advanced version", err)
			}
		})
	}
}
