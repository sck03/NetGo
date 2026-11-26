package session

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
	"time"
)

type blockedPeers struct {
	Peers
	entered, release chan struct{}
}

func (p blockedPeers) Verify(ctx context.Context, id string) (Binding, error) {
	close(p.entered)
	select {
	case <-p.release:
		return p.Peers.Verify(ctx, id)
	case <-ctx.Done():
		return Binding{}, ctx.Err()
	}
}

func TestSlowVerificationDoesNotBlockRevocation(t *testing.T) {
	for _, renewal := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquire", true: "renew"}[renewal], func(t *testing.T) {
			m, _, c, _ := fixture(t)
			var lease model.Lease
			if renewal {
				var err error
				lease, err = m.Acquire(context.Background(), c, "p1", 1, false, "initial")
				if err != nil {
					t.Fatal(err)
				}
			}
			p := blockedPeers{m.Peers, make(chan struct{}), make(chan struct{})}
			m.Peers = p
			result := make(chan error, 1)
			go func() {
				var err error
				if renewal {
					_, err = m.Renew(context.Background(), c, lease.ID, 1)
				} else {
					_, err = m.Acquire(context.Background(), c, "p1", 1, false, "waiting")
				}
				result <- err
			}()
			<-p.entered
			revoked := make(chan error, 1)
			go func() {
				revoked <- m.MutateUser(context.Background(), c.UserID, func(st *model.State) error { return st.InvalidateUser(c.UserID) })
			}()
			select {
			case err := <-revoked:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(time.Second):
				t.Error("upstream verification blocked revocation")
			}
			close(p.release)
			if err := <-result; !errors.Is(err, model.ErrUnauthorized) {
				t.Errorf("stale credential accepted: %v", err)
			}
		})
	}
}
