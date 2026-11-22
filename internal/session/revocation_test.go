package session

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
)

type peerLookup func(context.Context, string) (Binding, error)

func (f peerLookup) Verify(ctx context.Context, id string) (Binding, error) { return f(ctx, id) }

func TestRevokedCredentialCannotFinishPendingAcquire(t *testing.T) {
	m, gate, credential, _ := fixture(t)
	peers := m.Peers
	m.Peers = peerLookup(func(ctx context.Context, id string) (Binding, error) {
		if err := m.Store.Update(func(st *model.State) error {
			delete(st.Credentials, credential.TokenHash)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return peers.Verify(ctx, id)
	})
	if _, err := m.Acquire(context.Background(), credential, "p1", 1, false, "pending"); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal("revoked request obtained a lease", err)
	}
	if gate.calls != 0 {
		t.Fatal("revoked request touched the network gate")
	}
}

func TestRevokedCredentialCannotRenewOrReplayAcquire(t *testing.T) {
	m, gate, credential, _ := fixture(t)
	ctx := context.Background()
	lease, err := m.Acquire(ctx, credential, "p1", 1, false, "same-operation")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Update(func(st *model.State) error {
		delete(st.Credentials, credential.TokenHash)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := gate.calls
	if _, err := m.Renew(ctx, credential, lease.ID, 1); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal("revoked credential renewed access", err)
	}
	if _, err := m.Acquire(ctx, credential, "p1", 1, false, "same-operation"); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal("idempotent retry bypassed revocation", err)
	}
	if gate.calls != calls {
		t.Fatal("revoked credential changed the gate")
	}
}
