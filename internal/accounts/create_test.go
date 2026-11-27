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
	"path/filepath"
	"testing"
	"time"
)

func TestCreateCompensationSurvivesRestartAndProtectsCommittedUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	credential := model.Credential{TokenHash: "admin", UserID: "admin", DeviceID: "browser", ExpiresAt: time.Now().Add(time.Hour)}
	if err = db.Update(func(st *model.State) error {
		st.Users["admin"] = model.User{ID: "admin", Role: "admin"}
		st.Devices["browser"] = model.Device{ID: "browser", UserID: "admin"}
		st.Credentials[credential.TokenHash] = credential
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	status, deletes := http.StatusServiceUnavailable, 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`[]`))
			return
		}
		if r.Method == "POST" {
			if err := db.Update(func(st *model.State) error { delete(st.Credentials, credential.TokenHash); return nil }); err != nil {
				t.Error(err)
			}
			w.Write([]byte(`{"id":"created","password":"temporary"}`))
		} else {
			deletes++
			w.WriteHeader(status)
		}
	}))
	defer up.Close()
	s := Service{Store: db, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	if _, err = s.Create(authorization.WithAdmin(context.Background(), credential), NewUser{Alias: "new", Email: "new@example.test"}); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal(err)
	}
	item, err := db.AccountCleanup("created")
	if err != nil || item.CleanupAttempts != 1 || deletes != 1 {
		t.Fatal("failed compensation was not durable", item, err, deletes)
	}
	if _, err = db.User("created"); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("created local user after authorization failed", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Store = db
	if err = s.Retry(context.Background()); err != nil || deletes != 1 {
		t.Fatal("ignored retry backoff", err, deletes)
	}
	status = http.StatusNotFound
	// Exercise the same retry body immediately without sleeping through backoff.
	if err = s.cleanupCreated(context.Background(), "created"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AccountCleanup("created"); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("completed job retained", err)
	}
	if err = db.ReserveAccountCreation(store.AccountCleanup{ID: "committed"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Update(func(st *model.State) error { st.Users["committed"] = model.User{ID: "committed"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = s.Retry(context.Background()); err != nil || deletes != 2 {
		t.Fatal("committed user deleted by orphan recovery", err, deletes)
	}
	if _, err = db.User("committed"); err != nil {
		t.Fatal(err)
	}
	if err = db.ReserveAccountCreation(store.AccountCleanup{ID: "committed"}); !errors.Is(err, model.ErrConflict) {
		t.Fatal("existing account ownership claimed", err)
	}
}
