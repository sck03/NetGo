package remote

import (
	"context"
	"errors"
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
			got, err := f.m.List(f.c)
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
	got, err := f.m.List(f.c)
	if err != nil || got.Hosts[0].Status != "busy" {
		t.Fatalf("busy: %+v %v", got, err)
	}
	if _, err = f.m.Acquire(context.Background(), f.c, Request{HostID: "host", Generation: 2, Capabilities: 1}, "another-operation-key"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy acquisition: %v", err)
	}
	if err = f.m.Revoke(f.c, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	got, err = f.m.List(f.c)
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
