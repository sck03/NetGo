package remote

import (
	"encoding/json"
	"fmt"
	"netgo/internal/model"
	"testing"
)

func TestInventoryExcludesHistoryAndHistoryIsBounded(t *testing.T) {
	f := setup(t)
	a := f.acquire(t)
	if err := f.m.Store.RemoteUpdate(func(st *model.State) error {
		for i := 0; i < 2500; i++ {
			r := a.Session
			r.ID = fmt.Sprintf("closed-session-%08d", i)
			r.Generation = uint64(i + 2)
			r.State = "closed"
			r.ControllerAuth = "secret"
			st.RemoteSessions[r.ID] = r
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inv, err := f.m.List(f.c)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(inv)
	if len(b) > 1<<20 || len(inv.Sessions) != 1 || inv.NextGeneration != 2502 {
		t.Fatal("history polluted inventory", len(b), inv.NextGeneration)
	}
	page, err := f.m.Store.RemoteHistory("", 50)
	if err != nil || len(page.Sessions) != 50 || page.NextBefore == "" {
		t.Fatal(page, err)
	}
	for _, r := range page.Sessions {
		if r.ControllerAuth != "" {
			t.Fatal("credential leaked")
		}
	}
	next, err := f.m.Store.RemoteHistory(page.NextBefore, 50)
	if err != nil || len(next.Sessions) != 50 || next.Sessions[0].ID >= page.NextBefore {
		t.Fatal("cursor repeated history", err)
	}
}
