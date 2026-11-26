package devices

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/store"
	"sync"
	"testing"
	"time"
)

type statusError int

func (e statusError) Error() string   { return fmt.Sprint(int(e)) }
func (e statusError) HTTPStatus() int { return int(e) }

type upstream struct {
	status int
	db     *store.Store
	t      *testing.T
}

func (u *upstream) Request(context.Context, string, string, any, any) (http.Header, error) {
	st, err := u.db.View()
	if err != nil || len(st.Credentials) != 0 || len(st.Leases) != 0 || !st.Devices["d"].DeletePending {
		u.t.Fatal("upstream called before local revocation", err)
	}
	if u.status != 204 {
		return nil, statusError(u.status)
	}
	return nil, nil
}

type gate struct{}

func (gate) Apply(context.Context, []model.Lease) error { return nil }

func TestSlowCleanupDoesNotBlockAnotherDevice(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u"}
		for _, id := range []string{"slow", "fast"} {
			st.Devices[id] = model.Device{ID: id, UserID: "u", PeerID: id}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/peers/slow" {
			close(entered)
			<-release
		}
		w.WriteHeader(204)
	}))
	defer up.Close()
	defer unblock()
	s := Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}, Management: &netbird.Management{URL: up.URL, Client: up.Client()}}
	done := make(chan error, 1)
	go func() { _, err := s.Delete(context.Background(), "slow"); done <- err }()
	<-entered
	fast := make(chan error, 1)
	go func() { _, err := s.Delete(context.Background(), "fast"); fast <- err }()
	select {
	case err := <-fast:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated deletion blocked on slow upstream")
	}
	result, err := s.Delete(context.Background(), "slow")
	if err != nil || !result.CleanupPending {
		t.Fatal("concurrent cleanup was not coalesced", result, err)
	}
	unblock()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestDeleteRevokesBeforeUpstreamAndRetries(t *testing.T) {
	for _, code := range []int{404, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			db, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Update(func(st *model.State) error {
				st.Users["u"] = model.User{ID: "u"}
				st.Devices["d"] = model.Device{ID: "d", UserID: "u", PeerID: "peer"}
				st.Credentials["token"] = model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
				st.Leases["l"] = model.Lease{ID: "l", UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			remote := &upstream{code, db, t}
			service := &Service{Store: db, Sessions: &session.Manager{Store: db, Gate: gate{}}, Management: remote}
			result, err := service.Delete(context.Background(), "d")
			if err != nil {
				t.Fatal(err)
			}
			if result.CleanupPending != (code == 503) {
				t.Fatal(result)
			}
			remote.status = 204
			service.Now = func() time.Time { return time.Now().Add(time.Hour) }
			if err = service.Retry(context.Background()); err != nil {
				t.Fatal(err)
			}
			state, _ := db.View()
			if len(state.Devices) != 0 {
				t.Fatal("tombstone not removed")
			}
		})
	}
}
