package remote

import (
	"context"
	"fmt"
	"netgo/internal/model"
	"netgo/internal/remote/ticket"
	"testing"
	"time"
)

func TestExpiredSessionReplacementKeepsHostUnique(t *testing.T) {
	f := setup(t)
	last := ""
	for i := uint64(1); i <= 128; i++ {
		if last != "" {
			if e := f.m.Store.RemoteUpdate(func(st *model.State) error {
				r := st.RemoteSessions[last]
				r.ExpiresAt = f.now.Add(-time.Second)
				st.RemoteSessions[last] = r
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
		a, e := f.m.Acquire(context.Background(), f.c, Request{HostID: "host", Generation: i, Capabilities: ticket.CapabilityView}, fmt.Sprintf("replacement-key-%016d", i))
		if e != nil {
			t.Fatalf("replacement %d: %v", i, e)
		}
		last = a.Session.ID
	}
	st, e := f.m.Store.View()
	if e != nil {
		t.Fatal(e)
	}
	active := 0
	for _, r := range st.RemoteSessions {
		if r.State != "closed" {
			active++
		}
	}
	if active != 1 {
		t.Fatal(active)
	}
}
func TestDefaultLimitAndExecutableHostContract(t *testing.T) {
	f := setup(t)
	if e := f.m.Store.Update(func(st *model.State) error {
		u := st.Users[f.c.UserID]
		u.MaxSessions = 0
		st.Users[u.ID] = u
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	f.acquire(t)
	for _, h := range []model.Host{
		{ID: "host", Name: "x", Platform: "linux", Boot: f.boot, Port: 29990, Capabilities: 1, RequireConsent: true},
		{ID: "host", Name: "x", Platform: "windows", Boot: f.boot, Port: 29990, Capabilities: 9, RequireConsent: true},
		{ID: "host", Name: "x", Platform: "windows", Boot: f.boot, Port: 29990, Capabilities: 1},
	} {
		if _, e := f.m.Register(f.h, h); e == nil {
			t.Fatal("accepted unsupported host", h)
		}
	}
}
