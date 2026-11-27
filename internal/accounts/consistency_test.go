package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestLostCreationResponseRecoveredAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, deleted := false, false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			if created {
				w.Write([]byte(`[{"id":"upstream","email":"new@example.test","name":"New"}]`))
			} else {
				w.Write([]byte(`[]`))
			}
		case "POST":
			pending, e := db.PendingAccountCleanups(time.Now().Add(time.Hour), 10)
			if e != nil || len(pending) != 1 || !pending[0].Pending {
				t.Error("POST preceded durable intent", pending, e)
			}
			created = true
			w.Write([]byte(`{"id":"upstream",`))
		case "DELETE":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer upstream.Close()
	service := Service{Store: db, Management: &netbird.Management{URL: upstream.URL, Client: upstream.Client()}}
	if _, err = service.Create(context.Background(), NewUser{Alias: "new", Email: "new@example.test", Name: "New"}); err == nil {
		t.Fatal("accepted incomplete response")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service.Store = db
	pending, err := db.PendingAccountCleanups(time.Now().Add(time.Hour), 10)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if err = service.recoverCreation(context.Background(), pending[0]); err != nil || !deleted {
		t.Fatal("orphan not recovered", deleted, err)
	}
	pending, err = db.PendingAccountCleanups(time.Now().Add(time.Hour), 10)
	if err != nil || len(pending) != 0 {
		t.Fatal("cleanup remains", pending, err)
	}
	if _, err = db.User("upstream"); !errors.Is(err, model.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCreateDoesNotDeletePreexistingUpstreamIdentity(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("mutated pre-existing identity", r.Method)
		}
		json.NewEncoder(w).Encode([]map[string]string{{"id": "existing", "email": "new@example.test"}})
	}))
	defer up.Close()
	service := Service{Store: db, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	if _, err = service.Create(context.Background(), NewUser{Alias: "new", Email: "NEW@example.test"}); !errors.Is(err, model.ErrConflict) {
		t.Fatal(err)
	}
}

func TestPermanentAdminAndMetadataOnlyUpdate(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u := model.User{ID: "admin", Alias: "admin", Email: "a@example.test", Name: "Admin", Role: "admin", MaxDevices: 3, MaxSessions: 1}
	if err = db.Update(func(st *model.State) error {
		st.Users[u.ID] = u
		st.Devices["device"] = model.Device{ID: "device", UserID: u.ID}
		st.Credentials["credential"] = model.Credential{UserID: u.ID, DeviceID: "device", ExpiresAt: time.Now().Add(time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}}
	expiring := u
	expiring.ExpiresAt = time.Now().Add(time.Hour)
	if _, err = service.Update(context.Background(), expiring); err == nil || err.Error() != "last_permanent_administrator" {
		t.Fatal("last admin may expire", err)
	}
	u.Name = "Changed"
	if _, err = service.Update(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	st, err := db.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Credentials) != 1 || st.Users[u.ID].AuthRevision != 0 {
		t.Fatal("rename revoked identity")
	}
	if err = db.Update(func(st *model.State) error { st.Users["backup"] = model.User{ID: "backup", Role: "admin"}; return nil }); err != nil {
		t.Fatal(err)
	}
	expiring.EditRevision = st.Users[u.ID].EditRevision
	if _, err = service.Update(context.Background(), expiring); err != nil {
		t.Fatal("backup admin not recognized", err)
	}
	st, _ = db.View()
	if len(st.Credentials) != 0 {
		t.Fatal("expiry edit did not revoke identity")
	}
}
