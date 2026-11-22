package identity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"netgo/internal/model"
	"testing"
)

// Disabling then re-enabling must not resurrect a transaction whose password
// was checked before the administrator's change.
func TestUserChangeRejectsPreviouslyAuthorizedExchange(t *testing.T) {
	n, private, verifier, begin, _ := nativeFixture(t)
	tx, err := n.Begin(begin)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(message string) string {
		return base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(message)))
	}
	if err = n.Complete(context.Background(), tx.ID, "alice", "correct-password", sign(Proof(tx))); err != nil {
		t.Fatal(err)
	}
	if err = n.Store.Update(func(st *model.State) error { return st.InvalidateUser("u") }); err != nil {
		t.Fatal(err)
	}
	if _, err = n.Exchange(context.Background(), tx.ID, verifier, sign(Proof(tx)+"\nexchange\n"+verifier)); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("revoked authentication exchanged: %v", err)
	}
	state, err := n.Store.View()
	if err != nil || len(state.Credentials) != 0 || len(state.Devices) != 0 {
		t.Fatalf("unexpected state after rejection: %v", err)
	}
	// A new password authentication after the change is allowed.
	tx, err = n.Begin(begin)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Complete(context.Background(), tx.ID, "alice", "correct-password", sign(Proof(tx))); err != nil {
		t.Fatal(err)
	}
	if _, err = n.Exchange(context.Background(), tx.ID, verifier, sign(Proof(tx)+"\nexchange\n"+verifier)); err != nil {
		t.Fatal(err)
	}
}
