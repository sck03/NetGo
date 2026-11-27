package mobile

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type testActivity struct{ atomic.Bool }

func (a *testActivity) ControlActive() bool { return a.Load() }

func TestRemoteCancellationIsScopedAndCleanupBounded(t *testing.T) {
	a := &testActivity{}
	a.Store(true)
	ctx, cancel := remoteContext(a, false)
	defer cancel()
	other, stop := remoteContext(nil, false)
	defer stop()
	a.Store(false)
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal(ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("native cancellation did not reach request")
	}
	if other.Err() != nil {
		t.Fatal("unrelated request cancelled")
	}
	cleanup, finish := remoteContext(a, true)
	defer finish()
	deadline, _ := cleanup.Deadline()
	if cleanup.Err() != nil || time.Until(deadline) > 2*time.Second {
		t.Fatal("cleanup cancelled or unbounded")
	}
	ctx, cancel = remoteContext(a, false)
	defer cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("cancel-before-start accepted")
	}
}
