package remote

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestInventoryUsesServerStateAndEffectiveGrants(t *testing.T) {
	cases := []struct {
		name, status string
		caps         uint32
		change       func(*fixture, *model.State)
	}{
		{"ready", "ready", 3, func(f *fixture, s *model.State) {}},
		{"stale heartbeat", "offline", 3, func(f *fixture, s *model.State) {
			h := s.Hosts["host"]
			h.LastSeen = f.now.Add(-HeartbeatTimeout)
			s.Hosts[h.ID] = h
		}},
		{"future heartbeat", "offline", 3, func(f *fixture, s *model.State) {
			h := s.Hosts["host"]
			h.LastSeen = f.now.Add(time.Hour)
			s.Hosts[h.ID] = h
		}},
		{"disabled", "disabled", 3, func(f *fixture, s *model.State) { h := s.Hosts["host"]; h.Enabled = false; s.Hosts[h.ID] = h }},
		{"VPN expired", "vpn_offline", 3, func(f *fixture, s *model.State) { l := s.Leases["lh"]; l.ExpiresAt = f.now; s.Leases[l.ID] = l }},
		{"owner disabled", "vpn_offline", 3, func(f *fixture, s *model.State) { u := s.Users["owner"]; u.Disabled = true; s.Users[u.ID] = u }},
		{"grant expired", "ready", 0, func(f *fixture, s *model.State) {
			for id, g := range s.RemoteGrants {
				g.ExpiresAt = f.now
				s.RemoteGrants[id] = g
			}
		}},
		{"admin without grant", "ready", 0, func(f *fixture, s *model.State) { clear(s.RemoteGrants) }},
		{"host capabilities reduced", "ready", 1, func(f *fixture, s *model.State) { h := s.Hosts["host"]; h.Capabilities = 1; s.Hosts[h.ID] = h }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			if err := f.m.Store.Update(func(s *model.State) error { tc.change(f, s); return nil }); err != nil {
				t.Fatal(err)
			}
			got, err := f.m.List(f.c, "")
			if err != nil || len(got.Hosts) != 1 {
				t.Fatalf("inventory: %+v %v", got, err)
			}
			h := got.Hosts[0]
			if h.Status != tc.status || h.AvailableCapabilities != tc.caps {
				t.Fatalf("summary: %+v", h)
			}
		})
	}
}

func TestInventoryBusyAndRevocation(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	got, err := f.m.List(f.c, "")
	if err != nil || got.Hosts[0].Status != "busy" {
		t.Fatalf("busy: %+v %v", got, err)
	}
	if _, err = f.m.Acquire(context.Background(), f.c, Request{HostID: "host", Generation: 2, Capabilities: 1}, "another-operation-key"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy acquisition: %v", err)
	}
	if err = f.m.Revoke(f.c, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	got, err = f.m.List(f.c, "")
	if err != nil || got.Hosts[0].Status != "ready" {
		t.Fatalf("revoke: %+v %v", got, err)
	}
}

func TestGrantRejectsUnimplementedHostCapabilities(t *testing.T) {
	f := setup(t)
	if _, err := f.m.Grant(f.c, model.RemoteGrant{HostID: "host", UserID: "user", Capabilities: 9}); err == nil {
		t.Fatal("clipboard permission accepted for view/input host")
	}
}

func TestAdminInventoryPagesAndSearchPreserveAuthorization(t *testing.T) {
	f := setup(t)
	if err := f.m.Store.Update(func(st *model.State) error {
		original := st.Hosts["host"]
		for i := 0; i < 60; i++ {
			id := fmt.Sprintf("page-%03d", i)
			st.Devices[id] = model.Device{ID: id, UserID: "owner"}
			h := original
			h.ID = id
			h.Name = id
			st.Hosts[id] = h
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := f.m.AdminList(f.c, "", "")
	if err != nil || len(first.Hosts) != 50 || first.NextAfter == "" {
		t.Fatalf("first page: %d %q %v", len(first.Hosts), first.NextAfter, err)
	}
	second, err := f.m.AdminList(f.c, first.NextAfter, "")
	if err != nil || len(second.Hosts) != 11 || second.NextAfter != "" {
		t.Fatalf("next page: %+v %v", second, err)
	}
	matched, err := f.m.AdminList(f.c, "", "workstation")
	if err != nil || len(matched.Hosts) != 1 || matched.Hosts[0].ID != "host" {
		t.Fatalf("search: %+v %v", matched, err)
	}
	if err := f.m.Store.Update(func(st *model.State) error { delete(st.Credentials, f.c.TokenHash); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.AdminList(f.c, "", ""); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("revoked credential accepted: %v", err)
	}
}

func TestRemoteSweepLoadsOnlySessionDependencies(t *testing.T) {
	f := setup(t)
	f.acquire(t)
	if err := f.m.Store.Update(func(st *model.State) error {
		st.Users["idle"] = model.User{ID: "idle"}
		st.Devices["idle"] = model.Device{ID: "idle", UserID: "idle"}
		st.Credentials["idle"] = model.Credential{UserID: "idle", DeviceID: "idle"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(LeaseDuration + time.Second)
	if err := f.m.Store.RemoteSweep(f.now, func(st *model.State) error {
		if _, ok := st.Users["idle"]; ok {
			t.Fatal("idle account loaded into sweep")
		}
		if len(st.Devices) != 2 || len(st.Credentials) != 1 || len(st.Leases) != 2 {
			t.Fatal("incomplete session dependency graph")
		}
		f.m.sweep(st, f.now)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := f.m.Store.View()
	if err != nil || st.Credentials["idle"].DeviceID != "idle" {
		t.Fatalf("unrelated credential changed: %v", err)
	}
	for _, session := range st.RemoteSessions {
		if session.State != "closed" {
			t.Fatal("expired session not closed")
		}
	}
}

func TestNativeInventoryPagesAreBoundedAndKeepOwnSessions(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if err := f.m.Store.Update(func(st *model.State) error {
		original := st.Hosts["host"]
		for i := 0; i < 61; i++ {
			id := fmt.Sprintf("native-%03d", i)
			h := original
			h.ID = id
			st.Hosts[id] = h
			st.Devices[id] = model.Device{ID: id, UserID: "owner"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	after := ""
	for {
		page, err := f.m.List(f.c, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Hosts) > 50 || len(page.Sessions) != 1 || page.Sessions[0].ID != a.Session.ID {
			t.Fatalf("unbounded or incomplete native page: %+v", page)
		}
		for _, h := range page.Hosts {
			if seen[h.ID] {
				t.Fatalf("duplicate host %s", h.ID)
			}
			seen[h.ID] = true
		}
		if page.NextAfter == "" {
			break
		}
		if page.NextAfter <= after {
			t.Fatal("cursor did not advance")
		}
		after = page.NextAfter
	}
	if len(seen) != 62 {
		t.Fatalf("missing hosts: %d", len(seen))
	}
	if err := f.m.Store.Update(func(st *model.State) error {
		u := st.Users[f.c.UserID]
		u.Role = "user"
		st.Users[u.ID] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := f.m.List(f.c, "")
	if err != nil || len(page.Hosts) != 1 || page.Hosts[0].ID != "host" {
		t.Fatalf("ungranted hosts leaked: %+v %v", page, err)
	}
}
