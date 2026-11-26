package store

import (
	"fmt"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestAdminSummaryAndPages(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err = s.Update(func(st *model.State) error {
		for i := 0; i < 55; i++ {
			id := fmt.Sprintf("%03d", i)
			st.Users[id] = model.User{ID: id}
			st.Devices[id] = model.Device{ID: id, UserID: id}
			st.Leases[id] = model.Lease{ID: id, UserID: id, DeviceID: id, ExpiresAt: now.Add(time.Minute)}
		}
		st.Resources["lan"] = model.Resource{ID: "lan", Protocol: "tcp"}
		st.Resources["exit"] = model.Resource{ID: "exit", Protocol: "internet"}
		u := st.Users["000"]
		u.Disabled = true
		st.Users[u.ID] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, counts, err := s.AdminSummary(now)
	if err != nil || len(st.Users) != 0 || counts["leases"] != 54 || counts["users"] != 55 || counts["resources"] != 1 {
		t.Fatal(counts, err)
	}
	page, next, err := s.AdminPage("users", "", 50)
	if err != nil || len(page.Users) != 50 || next != "049" || len(page.Resources) != 2 {
		t.Fatal(next, err)
	}
	page, next, err = s.AdminPage("users", next, 50)
	if err != nil || len(page.Users) != 5 || next != "" {
		t.Fatal(next, err)
	}
	page, _, err = s.AdminPage("sessions", "049", 50)
	if err != nil || len(page.Devices) != 5 || len(page.Users) != 5 || len(page.Leases) != 5 {
		t.Fatal("missing session owner labels", err)
	}
}

func TestSessionPagesAreIndependentOfIdleDevices(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Alias: "owner"}
		for i := 0; i < 51; i++ {
			id := fmt.Sprintf("d%03d", i)
			st.Devices[id] = model.Device{ID: id, UserID: "u"}
		}
		st.Leases["last"] = model.Lease{ID: "last", UserID: "u", DeviceID: "d050", ExpiresAt: time.Now().Add(time.Hour)}
		st.Leases["expired"] = model.Lease{ID: "expired", UserID: "u", DeviceID: "d000", ExpiresAt: time.Now().Add(-time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, next, err := s.AdminPage("sessions", "", 50)
	if err != nil || len(st.Leases) != 1 || st.Leases["last"].DeviceID != "d050" || len(st.Users) != 1 || next != "" {
		t.Fatalf("session missing: %+v %q %v", st.Leases, next, err)
	}
	st, next, err = s.AdminPage("devices", "", 50)
	if err != nil || len(st.Devices) != 50 || len(st.Leases) != 0 || next != "d049" {
		t.Fatal("device pagination mixed with sessions", next, err)
	}
	st, next, err = s.AdminPage("devices", next, 50)
	if err != nil || len(st.Devices) != 1 || next != "" {
		t.Fatal("last device missing", err)
	}
}
