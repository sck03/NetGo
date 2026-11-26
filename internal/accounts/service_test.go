package accounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/store"
	"testing"
	"time"
)

type gate struct{}

func (gate) Apply(context.Context, []model.Lease) error { return nil }
func TestUpstreamCreateDoesNotBlockStateReads(t *testing.T) {
	db, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
