package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"netgo/internal/authorization"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gate struct{}

func (gate) Apply(context.Context, []model.Lease) error { return nil }

func TestBlockedCreateAllowsRevocationAndRechecksActor(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	credential := model.Credential{TokenHash: "admin-token", UserID: "admin", DeviceID: "browser", ExpiresAt: time.Now().Add(time.Hour)}
	target := model.User{ID: "target", Alias: "target", Email: "target@example.test", Role: "user", MaxDevices: 3, MaxSessions: 1}
	if err = db.Update(func(st *model.State) error {
		st.Users["admin"] = model.User{ID: "admin", Alias: "admin", Role: "admin"}
		st.Users[target.ID] = target
		st.Devices["browser"] = model.Device{ID: "browser", UserID: "admin"}
		st.Credentials[credential.TokenHash] = credential
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	var compensated atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`[]`))
			return
		}
		if r.Method == "POST" {
			close(entered)
			<-release
			w.Write([]byte(`{"id":"created","password":"temporary"}`))
		} else {
			compensated.Store(true)
			w.WriteHeader(204)
		}
	}))
	defer up.Close()
	defer unblock()
	service := Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	done := make(chan error, 1)
	go func() {
		_, err := service.Create(authorization.WithAdmin(context.Background(), credential), NewUser{Alias: "new", Email: "new@example.test"})
		done <- err
	}()
	<-entered
	changed := make(chan error, 1)
	target.Disabled = true
	go func() { _, err := service.Update(context.Background(), target); changed <- err }()
	select {
	case err := <-changed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow create blocked another account revocation")
	}
	if _, err = service.Create(context.Background(), NewUser{Alias: "NEW", Email: "other@example.test"}); !errors.Is(err, model.ErrConflict) {
		t.Fatal("duplicate reservation", err)
	}
	if err = db.Update(func(st *model.State) error { delete(st.Credentials, credential.TokenHash); return nil }); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err = <-done; !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal("revoked administrator committed create", err)
	}
	if !compensated.Load() {
		t.Fatal("failed create did not compensate upstream")
	}
}
func TestUpstreamCreateDoesNotBlockStateReads(t *testing.T) {
	db, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`[]`))
			return
		}
		close(entered)
		<-release
		w.Write([]byte(`{"id":"u","password":"one-time"}`))
	}))
	defer up.Close()
	service := Service{Store: db, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	done := make(chan error, 1)
	go func() {
		_, e := service.Create(context.Background(), NewUser{Alias: "alice", Email: "a@example.test"})
		done <- e
	}()
	<-entered
	read := make(chan error, 1)
	go func() { _, e := db.View(); read <- e }()
	select {
	case e := <-read:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(time.Second):
		t.Error("upstream IO held database lock")
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestDeleteFailureIsRevokedAndRetryable(t *testing.T) {
	db, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	e = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Role: "user"}
		st.Devices["d"] = model.Device{ID: "d", UserID: "u"}
		st.Credentials["c"] = model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	status := http.StatusServiceUnavailable
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer up.Close()
	s := Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	if result, err := s.Delete(context.Background(), "u"); err != nil || !result.CleanupPending {
		t.Fatal("missing pending cleanup", result, err)
	}
	st, _ := db.View()
	if !st.Users["u"].Disabled || !st.Users["u"].DeletePending || len(st.Credentials) != 0 {
		t.Fatal("delete failure kept access")
	}
	status = http.StatusNotFound
	if _, e = s.Delete(context.Background(), "u"); e != nil {
		t.Fatal(e)
	}
	st, _ = db.View()
	if len(st.Users) != 0 || len(st.Devices) != 0 {
		t.Fatal("retry left orphan state")
	}
}

func TestLoginNameIsImmutableAndDisplayNameIsEditable(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := model.User{ID: "u", Alias: "login", Email: "user@example.test", Role: "user", MaxDevices: 1, MaxSessions: 1}
	err = db.Update(func(st *model.State) error {
		st.Users[user.ID] = user
		st.Devices["d"] = model.Device{ID: "d", UserID: user.ID}
		st.Credentials["token"] = model.Credential{UserID: user.ID, DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
		st.Leases["lease"] = model.Lease{ID: "lease", UserID: user.ID, DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}}
	changed := user
	changed.Alias = "renamed"
	if _, err = service.Update(context.Background(), changed); err == nil || err.Error() != "alias_immutable" {
		t.Fatalf("rename accepted: %v", err)
	}
	st, err := db.View()
	if err != nil || st.Users[user.ID].Alias != "login" || len(st.Credentials) != 1 || len(st.Leases) != 1 {
		t.Fatalf("rejected rename changed identity or sessions: %v", err)
	}
	user.Name = "New display name"
	saved, err := service.Update(context.Background(), user)
	if err != nil || saved.Name != user.Name || saved.Alias != "login" {
		t.Fatalf("display name update: %+v %v", saved, err)
	}
}
