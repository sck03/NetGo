package client

import (
	"context"
	"testing"
	"time"
)

type cleanupEngine struct {
	Engine
	calls int
}

func (e *cleanupEngine) Down(context.Context) error { e.calls++; return nil }

func TestCleanupTimeoutRetainsWorkerFence(t *testing.T) {
	e := &cleanupEngine{}
	done := make(chan struct{})
	c := &Controller{Engine: e, done: done}
	for range 2 {
		if err := c.disconnectWithin(time.Millisecond); err == nil {
			t.Fatal("live worker accepted as cleaned up")
		}
		if c.done != done || !c.cleanupFailed || e.calls != 0 {
			t.Fatal("worker fence lost")
		}
	}
	close(done)
	if err := c.disconnectWithin(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if c.done != nil || c.cleanupFailed || c.Status().State != "disconnected" || e.calls != 1 {
		t.Fatal("completed cleanup did not recover")
	}
}
