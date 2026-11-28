package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestInventoryExcludesHistoryAndHistoryIsBounded(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if err := f.m.Store.Update(func(st *model.State) error {
		for i := 0; i < 2500; i++ {
			r := a.Session
			r.ID = fmt.Sprintf("closed-session-%08d", i)
			r.Generation = uint64(i + 2)
			r.Close(f.now)
			r.ControllerAuth = "secret"
			st.RemoteSessions[r.ID] = r
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inv, err := f.m.List(context.Background(), f.c, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(inv)
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(b, &payload)
	if payload["grants"] != nil || payload["sessions"] != nil {
		t.Fatal("unused native response fields", string(b))
	}
	if len(b) > 1<<20 || inv.NextGeneration != 2502 {
		t.Fatal("history polluted inventory", len(b), inv.NextGeneration)
	}
	page, err := f.m.Store.RemoteHistory(context.Background(), "", 50)
	if err != nil || len(page.Sessions) != 50 || page.NextBefore == "" {
		t.Fatal(page, err)
	}
	for _, r := range page.Sessions {
		if r.ControllerAuth != "" {
			t.Fatal("credential leaked")
		}
	}
	next, err := f.m.Store.RemoteHistory(context.Background(), page.NextBefore, 50)
	if err != nil || len(next.Sessions) != 50 || next.Sessions[0].ID >= page.Sessions[len(page.Sessions)-1].ID {
		t.Fatal("cursor repeated history", err)
	}
}

func TestHistoryUsesCreationTimeAndRetainsItAcrossRenewal(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if _, err := f.m.Consume(context.Background(), f.h, a.Session.ID, a.Session.Sequence, f.boot, proof(t, a, f.controller)); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Second)
	renewed, err := f.m.Renew(context.Background(), f.c, a.Session.ID, 1)
	if err != nil || !renewed.Session.CreatedAt.Equal(a.Session.CreatedAt) || !renewed.Session.IssuedAt.After(a.Session.IssuedAt) {
		t.Fatal("renew changed start time", err)
	}
	if err = f.m.Revoke(context.Background(), f.c, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.m.Store.Update(func(st *model.State) error {
		for i, id := range []string{"z-oldest", "a-newest", "m-middle"} {
			r := a.Session
			r.ID = id
			r.State = "closed"
			r.CreatedAt = f.now.Add(time.Duration([]int{-3, -1, -2}[i]) * time.Hour)
			r.ClosedAt = f.now
			st.RemoteSessions[id] = r
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := f.m.Store.RemoteHistory(context.Background(), "", 2)
	if err != nil || len(first.Sessions) != 2 || first.Sessions[0].ID != a.Session.ID || first.Sessions[1].ID != "a-newest" {
		t.Fatal("history not chronological", first, err)
	}
	if err = f.m.Store.Update(func(st *model.State) error { delete(st.RemoteSessions, "a-newest"); return nil }); err != nil {
		t.Fatal(err)
	}
	next, err := f.m.Store.RemoteHistory(context.Background(), first.NextBefore, 2)
	if err != nil || len(next.Sessions) != 2 || next.Sessions[0].ID != "m-middle" || next.Sessions[1].ID != "z-oldest" {
		t.Fatal("cursor depended on retained row", next, err)
	}
	if _, err = f.m.Store.RemoteHistory(context.Background(), "invalid!", 2); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
