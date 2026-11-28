package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"netgo/internal/authorization"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/store"
	"testing"
	"time"
)

func TestManualCreationResolutionRequiresAgeAbsenceAndLiveAdmin(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := model.Credential{TokenHash: "admin", UserID: "admin", DeviceID: "browser", ExpiresAt: time.Now().Add(time.Hour)}
	if err = db.Update(func(st *model.State) error {
		st.Users["admin"] = model.User{ID: "admin", Role: "admin"}
		st.Devices["browser"] = model.Device{ID: "browser", UserID: "admin"}
		st.Credentials[c.TokenHash] = c
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	found := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if found {
			w.Write([]byte(`[{"id":"existing","email":"new@test"}]`))
		} else {
			w.Write([]byte(`[]`))
		}
	}))
	defer up.Close()
	s := Service{Store: db, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	ctx := authorization.WithAdmin(context.Background(), c)
	item := store.AccountCleanup{ID: "pending", Alias: "new", Email: "new@test", Pending: true, CreatedAt: time.Now().UTC()}
	if err = db.ReserveAccountCreation(item); err != nil {
		t.Fatal(err)
	}
	if err = s.ManageCreation(ctx, item.ID, true); err == nil {
		t.Fatal("released running request")
	}
	if err = db.FinishAccountCleanup(item.ID); err != nil {
		t.Fatal(err)
	}
	item.CreatedAt = time.Now().Add(-11 * time.Minute).UTC()
	if err = db.ReserveAccountCreation(item); err != nil {
		t.Fatal(err)
	}
	found = true
	if err = s.ManageCreation(ctx, item.ID, true); err == nil {
		t.Fatal("released existing upstream account")
	}
	found = false
	if err = s.ManageCreation(ctx, item.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AccountCleanup(item.ID); err != nil {
		t.Fatal("empty lookup released uncertain reservation", err)
	}
	if err = s.ManageCreation(ctx, item.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AccountCleanup(item.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("confirmed reservation retained", err)
	}
	if err = db.ReserveAccountCreation(item); err != nil {
		t.Fatal(err)
	}
	if err = db.Update(func(st *model.State) error { delete(st.Credentials, c.TokenHash); return nil }); err != nil {
		t.Fatal(err)
	}
	if err = s.ManageCreation(ctx, item.ID, true); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal("revoked administrator resolved job", err)
	}
}
