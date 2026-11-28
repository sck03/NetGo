package store

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/model"
	"reflect"
	"testing"
	"time"
)

func TestActiveUpdatesPreserveHistoryAndInvalidateRoutes(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			s, _ := renewalFixture(t)
			err := s.Update(func(st *model.State) error {
				for i := 0; i < 100; i++ {
					r := st.RemoteSessions["remote"]
					r.ID = fmt.Sprintf("history-%d", i)
					r.Close(time.Now())
					st.RemoteSessions[r.ID] = r
				}
				st.RemoteOperations["keep"] = model.RemoteOperation{SessionID: "history-0"}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := s.View()
			write := func(fn func(*model.State) error) error { return s.VPNUpdate(context.Background(), fn) }
			if policy {
				write = func(fn func(*model.State) error) error { return s.RemotePolicyUpdate(context.Background(), fn) }
			}
			mutation := func(st *model.State) error {
				if len(st.RemoteSessions) != 1 || len(st.RemoteOperations) != 0 {
					t.Fatal("retained history loaded on active path")
				}
				if len(model.RemoteRoutes(*st)) != 1 {
					t.Fatal("active route dependency missing")
				}
				if policy {
					delete(st.RemoteGrants, "g")
					r := st.RemoteSessions["remote"]
					r.Close(time.Now())
					st.RemoteSessions[r.ID] = r
				} else {
					delete(st.Leases, "0")
				}
				return nil
			}
			failure := errors.New("rollback")
			if err := write(func(st *model.State) error { mutation(st); return failure }); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			after, _ := s.View()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed scoped update committed")
			}
			if err := write(mutation); err != nil {
				t.Fatal(err)
			}
			after, _ = s.View()
			if after.Revision != before.Revision+1 || len(model.RemoteRoutes(after)) != 0 {
				t.Fatal("route revision not invalidated")
			}
			if len(after.RemoteSessions) != len(before.RemoteSessions) || !reflect.DeepEqual(before.RemoteOperations, after.RemoteOperations) {
				t.Fatal("history/idempotency lost")
			}
			for id, r := range before.RemoteSessions {
				if r.State == "closed" && !reflect.DeepEqual(r, after.RemoteSessions[id]) {
					t.Fatal("closed history mutated")
				}
			}
		})
	}
}
