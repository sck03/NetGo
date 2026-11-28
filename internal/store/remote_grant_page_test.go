package store

import (
	"context"
	"fmt"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestAdminInventoryAndGrantPagesAreBounded(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := model.Credential{TokenHash: "admin", UserID: "admin", DeviceID: "browser", ExpiresAt: time.Now().Add(time.Hour)}
	err = db.Update(func(st *model.State) error {
		st.Users["admin"] = model.User{ID: "admin", Role: "admin"}
		st.Devices["browser"] = model.Device{ID: "browser", UserID: "admin"}
		st.Credentials[c.TokenHash] = c
		st.Devices["host"] = model.Device{ID: "host", UserID: "admin"}
		st.Hosts["host"] = model.Host{ID: "host", Name: "Host", Revision: 1}
		for i := 0; i < 1000; i++ {
			id := fmt.Sprintf("u%04d", i)
			st.Users[id] = model.User{ID: id, Alias: id}
			g := model.Hash(id + ":host")
			st.RemoteGrants[g] = model.RemoteGrant{ID: g, UserID: id, HostID: "host", EditToken: id}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := db.AdminRemotePage(context.Background(), c, "", "")
	if err != nil || len(st.RemoteGrants) != 0 || len(st.Users) != 1 {
		t.Fatal("inventory expanded grants", len(st.Users), err)
	}
	seen := map[string]bool{}
	after := ""
	for {
		st, next, err := db.AdminGrantPage(context.Background(), c, "host", "", after)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.RemoteGrants) > 50 || len(st.Users) > 51 {
			t.Fatal("unbounded page")
		}
		for id := range st.RemoteGrants {
			if seen[id] {
				t.Fatal("duplicate")
			}
			seen[id] = true
		}
		if next == "" {
			break
		}
		after = next
	}
	if len(seen) != 1000 {
		t.Fatal("missing grants", len(seen))
	}
	st, _, err = db.AdminGrantPage(context.Background(), c, "host", "u0999", "")
	if err != nil || len(st.RemoteGrants) != 1 || st.RemoteGrants[model.Hash("u0999:host")].EditToken != "u0999" {
		t.Fatal("off-page edit token missing", err)
	}
}
