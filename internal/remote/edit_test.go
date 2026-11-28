package remote

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
)

func TestGrantEditsFenceStaleFormsAndRecreation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.acquire(t)
	st, _ := f.m.Store.View()
	original := st.RemoteGrants[a.Session.GrantID]
	restricted := original
	restricted.Capabilities = 1
	saved, err := f.m.Grant(ctx, f.c, restricted)
	if err != nil || saved.EditToken == original.EditToken {
		t.Fatal(saved, err)
	}
	if _, err = f.m.Grant(ctx, f.c, original); !errors.Is(err, model.ErrEditConflict) {
		t.Fatal("stale grant accepted", err)
	}
	st, _ = f.m.Store.View()
	if st.RemoteGrants[original.ID].Capabilities != 1 || st.RemoteSessions[a.Session.ID].State != "closed" {
		t.Fatal("old access restored")
	}
	if err = f.m.DeleteGrant(ctx, f.c, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.Grant(ctx, f.c, saved); !errors.Is(err, model.ErrEditConflict) {
		t.Fatal("deleted grant restored", err)
	}
	restricted.EditToken = ""
	recreated, err := f.m.Grant(ctx, f.c, restricted)
	if err != nil || recreated.EditToken == original.EditToken {
		t.Fatal(recreated, err)
	}
	if _, err = f.m.Grant(ctx, f.c, original); !errors.Is(err, model.ErrEditConflict) {
		t.Fatal("old incarnation accepted", err)
	}
}
