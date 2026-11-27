package remote

import (
	"context"
	"errors"
	"netgo/internal/model"
	"netgo/internal/session"
	"testing"
)

type failingPeers struct{ err error }

func (p failingPeers) Verify(context.Context, string) (session.Binding, error) {
	return session.Binding{}, p.err
}

func TestRemoteRenewLookupOutagePreservesUnextendedSession(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if _, err := f.m.Consume(f.h, a.Session.ID, 1, f.boot, proof(t, a, f.controller)); err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{context.DeadlineExceeded, model.ErrNotFound} {
		f.m.Peers = failingPeers{cause}
		if _, err := f.m.Renew(context.Background(), f.c, a.Session.ID, 1); !errors.Is(err, cause) || errors.Is(err, model.ErrControlUnavailable) != errors.Is(cause, context.DeadlineExceeded) {
			t.Fatal("wrong renewal failure", err)
		}
	}
	st, err := f.m.Store.View()
	if err != nil {
		t.Fatal(err)
	}
	r := st.RemoteSessions[a.Session.ID]
	if r.State != "active" || !r.ExpiresAt.Equal(a.Session.ExpiresAt) || r.Sequence != a.Session.Sequence {
		t.Fatal("failed lookup changed authorization", r)
	}
}

func TestNoopGrantKeepsTicketValid(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	st, err := f.m.Store.View()
	if err != nil {
		t.Fatal(err)
	}
	g := st.RemoteGrants[a.Session.GrantID]
	updated, err := f.m.Grant(f.c, g)
	if err != nil || updated.Revision != g.Revision {
		t.Fatal(err)
	}
	if _, err = f.m.Consume(f.h, a.Session.ID, 1, f.boot, proof(t, a, f.controller)); err != nil {
		t.Fatal("no-op invalidated ticket", err)
	}
	g.Capabilities = 1
	if _, err = f.m.Grant(f.c, g); err != nil {
		t.Fatal(err)
	}
	st, err = f.m.Store.View()
	if err != nil || st.RemoteSessions[a.Session.ID].State != "closed" {
		t.Fatal("changed grant retained ticket", err)
	}
}
