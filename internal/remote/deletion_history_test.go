package remote

import (
	"netgo/internal/model"
	"testing"
)

func TestHistorySurvivesIdentityDeletionAndClosesActiveSession(t *testing.T) {
	for _, remove := range []string{"host", "controller", "owner", "user"} {
		t.Run(remove, func(t *testing.T) {
			f := setup(t)
			a := f.acquire(t)
			if err := f.m.Store.Update(func(st *model.State) error {
				if remove == "host" || remove == "controller" {
					delete(st.Devices, remove)
				} else {
					delete(st.Users, remove)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			history, err := f.m.Store.RemoteHistory("", 10)
			if err != nil || len(history.Sessions) != 1 {
				t.Fatal("history removed", history, err)
			}
			r := history.Sessions[0]
			if r.ID != a.Session.ID || r.State != "closed" || r.ClosedAt.IsZero() || r.ControllerAuth != "" {
				t.Fatal("invalid history", r)
			}
			st, err := f.m.Store.View()
			if err != nil {
				t.Fatal(err)
			}
			if len(model.RemoteRoutes(st)) != 0 {
				t.Fatal("deleted identity still authorized")
			}
		})
	}
}
