package store

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestRemoteTransactionsCancelWhilePoolBusy(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for name, call := range map[string]func(context.Context) error{
		"policy": func(ctx context.Context) error {
			return s.RemotePolicyUpdate(ctx, func(*model.State) error { t.Error("cancelled callback ran"); return nil })
		},
		"target": func(ctx context.Context) error {
			_, err := s.RemoteTargetView(ctx, model.Credential{}, "", "")
			return err
		},
		"acquire": func(ctx context.Context) error {
			return s.RemoteAcquireUpdate(ctx, model.Credential{}, "", "", time.Now(), func(*model.State, RemoteAcquireLimits) error { t.Error("cancelled callback ran"); return nil })
		},
		"creation": func(ctx context.Context) error { _, err := s.AccountCreationView(ctx, "alias", "mail"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := call(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestResourceChoicesPageAndSelectedLabels(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Update(func(st *model.State) error {
		for i := 0; i < 55; i++ {
			id := fmt.Sprintf("r%03d", i)
			st.Resources[id] = model.Resource{ID: id, Name: "NAS " + id}
		}
		st.Users["u"] = model.User{ID: "u", Resources: []string{"r054"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := s.AdminPage("users", "", 50)
	if err != nil || len(state.Resources) != 1 || state.Resources["r054"].ID == "" {
		t.Fatal(state.Resources, err)
	}
	p, err := s.ResourceChoices(context.Background(), "", "")
	if err != nil || len(p.Resources) != 50 || p.NextAfter != "r049" {
		t.Fatal(p, err)
	}
	p, err = s.ResourceChoices(context.Background(), p.NextAfter, "")
	if err != nil || len(p.Resources) != 5 || p.NextAfter != "" {
		t.Fatal(p, err)
	}
	p, err = s.ResourceChoices(context.Background(), "", "NAS r054")
	if err != nil || len(p.Resources) != 1 || p.Resources[0].ID != "r054" {
		t.Fatal(p, err)
	}
}
