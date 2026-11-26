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
	if err != nil || len(page.Devices) != 5 || len(page.Users) != 55 || len(page.Leases) != 55 {
		t.Fatal("missing session owner labels", err)
	}
}
