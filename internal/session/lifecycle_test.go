package session

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
)

type pausedPeer struct {
	entered, resume chan struct{}
	binding         Binding
}

func (p pausedPeer) Verify(context.Context, string) (Binding, error) {
	close(p.entered)
	<-p.resume
	return p.binding, nil
}

func TestCloseFencesInFlightRequests(t *testing.T) {
	for _, renew := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquire", true: "renew"}[renew], func(t *testing.T) {
			m, g, c, _ := fixture(t)
			var lease model.Lease
			if renew {
				var err error
				lease, err = m.Acquire(context.Background(), c, "p1", 1, false, model.ID())
				if err != nil {
					t.Fatal(err)
				}
			}
			p := pausedPeer{make(chan struct{}), make(chan struct{}), m.Peers.(fakePeers)["p1"]}
			m.Peers = p
			done := make(chan error, 1)
			go func() {
				var err error
				if renew {
					_, err = m.Renew(context.Background(), c, lease.ID, 1)
				} else {
					_, err = m.Acquire(context.Background(), c, "p1", 1, false, model.ID())
				}
				done <- err
			}()
			<-p.entered
			err := m.Close(context.Background())
			close(p.resume)
			if err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, ErrStopped) {
				t.Fatal("late acquire was not fenced", err)
			}
			st, err := m.Store.View()
			if err != nil {
				t.Fatal(err)
			}
			if len(st.Leases) != 0 || len(g.leases) != 0 {
				t.Fatal("shutdown reopened access")
			}
			if err = m.Reset(context.Background()); !errors.Is(err, ErrStopped) {
				t.Fatal("reset reopened manager", err)
			}
			if err = m.Close(context.Background()); err != nil {
				t.Fatal("close is not retryable", err)
			}
		})
	}
}

func TestFailedCloseRemainsStoppedAndCanRetryGateCleanup(t *testing.T) {
	m, gate, c, _ := fixture(t)
	if _, err := m.Acquire(context.Background(), c, "p1", 1, false, model.ID()); err != nil {
		t.Fatal(err)
	}
	gate.fail = true
	if err := m.Close(context.Background()); err == nil {
		t.Fatal("gate cleanup error was hidden")
	}
	if _, err := m.Acquire(context.Background(), c, "p1", 2, false, model.ID()); !errors.Is(err, ErrStopped) {
		t.Fatal("failed close reopened admission", err)
	}
	st, err := m.Store.View()
	if err != nil || len(st.Leases) != 0 {
		t.Fatal("failed close retained stored access", err)
	}
	gate.fail = false
	if err := m.Close(context.Background()); err != nil || len(gate.leases) != 0 {
		t.Fatal("retry did not close gate", err)
	}
}
