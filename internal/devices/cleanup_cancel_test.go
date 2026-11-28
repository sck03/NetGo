package devices

import (
	"context"
	"netgo/internal/model"
	"netgo/internal/store"
	"testing"
	"time"
)

func TestCancelledCleanupStillPersistsBackoff(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := model.Device{ID: "d", UserID: "u", PeerID: "peer", DeletePending: true}
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u"}
		st.Devices[d.ID] = d
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := Service{Store: db}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = service.cleanup(ctx, d); err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	d, err = db.Device(d.ID)
	if err != nil || !d.DeletePending || d.CleanupAttempts != 1 || !d.CleanupAfter.After(time.Now()) {
		t.Fatalf("cancelled cleanup lost its backoff: %+v, %v", d, err)
	}
}
