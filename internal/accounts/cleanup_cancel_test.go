package accounts

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
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Disabled: true, DeletePending: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := Service{Store: db}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = service.cleanup(ctx, "u"); err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	u, err := db.User("u")
	if err != nil || !u.DeletePending || u.CleanupAttempts != 1 || !u.CleanupAfter.After(time.Now()) {
		t.Fatalf("cancelled cleanup lost its backoff: %+v, %v", u, err)
	}
}
