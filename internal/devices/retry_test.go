package devices

import (
	"context"
	"net/http"
	"netgo/internal/model"
	"netgo/internal/store"
	"testing"
	"time"
)

type selectiveRemoval struct{ calls []string }

func (u *selectiveRemoval) Request(_ context.Context, _ string, path string, _ any, _ any) (http.Header, error) {
	u.calls = append(u.calls, path)
	if path == "/peers/bad" {
		return nil, statusError(503)
	}
	return nil, nil
}
func TestFailedDeviceDoesNotStarveLaterDevices(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u"}
		for id, peer := range map[string]string{"a": "bad", "b": "good"} {
			st.Devices[id] = model.Device{ID: id, UserID: "u", PeerID: peer, DeletePending: true}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	up := &selectiveRemoval{}
	s := &Service{Store: db, Management: up, Now: func() time.Time { return now }}
	if err = s.Retry(context.Background()); err == nil {
		t.Fatal("failure missing")
	}
	st, _ := db.View()
	if len(st.Devices) != 1 || st.Devices["a"].CleanupAttempts != 1 || !st.Devices["a"].CleanupAfter.After(now) || len(up.calls) != 2 {
		t.Fatal("later device starved", st.Devices, up.calls)
	}
	if err = s.Retry(context.Background()); err != nil || len(up.calls) != 2 {
		t.Fatal("backoff not honored", err)
	}
}
