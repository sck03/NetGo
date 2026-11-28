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
			if e := f.m.Store.Update(func(st *model.State) error {
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

func TestInventoryLoadsOnlyVisibleDependencies(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if err := f.m.Store.Update(func(st *model.State) error {
		u := st.Users[f.c.UserID]
		u.Role = "user"
		st.Users[u.ID] = u
		st.Users["unrelated"] = model.User{ID: "unrelated", Alias: "unrelated"}
		st.Devices["unrelated"] = model.Device{ID: "unrelated", UserID: "unrelated"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := f.m.Store.RemoteInventory(context.Background(), f.c, "")
	state := page.State
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Users["unrelated"]; ok {
		t.Fatal("loaded unrelated account")
	}
	if _, ok := state.Devices["unrelated"]; ok {
		t.Fatal("loaded unrelated device")
	}
	inventory, err := f.m.List(context.Background(), f.c, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.RemoteSessions) != 1 || state.RemoteSessions[a.Session.ID].ID != a.Session.ID {
		t.Fatal("incomplete authorization dependencies", inventory)
	}
	found := false
	for _, h := range inventory.Hosts {
		if h.ID == "host" {
			found = h.Status == "busy"
		}
	}
	if !found {
		t.Fatal("active host lost busy state")
	}
}
func TestExecutableHostContract(t *testing.T) {
	f := setup(t)
	st, e := f.m.Store.View()
	if e != nil {
		t.Fatal(e)
	}
	h := st.Hosts["host"]
	for _, platform := range []string{"windows", "macos", "linux"} {
		for _, consent := range []bool{true, false} {
			h.Platform = platform
			h.RequireConsent = consent
			h.Capabilities = SupportedCapabilities
			if _, e = f.m.Register(context.Background(), f.h, h); e != nil {
				t.Fatal(platform, consent, e)
			}
		}
	}
	for _, platform := range []string{"ios", "android", "unknown"} {
		h.Platform = platform
		if _, e = f.m.Register(context.Background(), f.h, h); e == nil {
			t.Fatal("unsupported host accepted", platform)
		}
	}
	h.Platform = "windows"
	h.TransportKey = ""
	if _, e = f.m.Register(context.Background(), f.h, h); e == nil {
		t.Fatal("missing transport key accepted")
	}
}

func TestUnattendedGrantAndTransportRotation(t *testing.T) {
	f := setup(t)
	st, _ := f.m.Store.View()
	h := st.Hosts["host"]
	h.RequireConsent = false
	h.Capabilities = SupportedCapabilities
	if _, e := f.m.Register(context.Background(), f.h, h); e != nil {
		t.Fatal(e)
	}
	g := st.RemoteGrants[model.Hash(f.c.UserID+":"+h.ID)]
	g.Capabilities = SupportedCapabilities
	if _, e := f.m.Grant(context.Background(), f.c, g); e != nil {
		t.Fatal(e)
	}
	a := f.acquire(t)
	if a.Session.RequireConsent || a.HostKey != h.TransportKey {
		t.Fatal("unattended or transport binding incorrect")
	}
	h.TransportKey = st.Devices["controller"].PublicKey
	if _, e := f.m.Register(context.Background(), f.h, h); e != nil {
		t.Fatal(e)
	}
	st, _ = f.m.Store.View()
	if st.RemoteSessions[a.Session.ID].State != "closed" {
		t.Fatal("rotating transport identity retained session")
	}
}
