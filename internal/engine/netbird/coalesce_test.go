package netbird

import (
	"context"
	"net/http"
	"net/http/httptest"
	"netgo/internal/model"
	"netgo/internal/store"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureCoalescesOnlyUnchangedPeersAndApplyRepairsDrift(t *testing.T) {
	db, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var changed atomic.Bool
	var policyCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/peers" {
			if changed.Load() {
				w.Write([]byte(`[{"id":"new","user_id":"u"}]`))
			} else {
				w.Write([]byte("[]"))
			}
			return
		}
		policyCalls.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	r := Reconciler{Store: db, Management: &Management{URL: server.URL, Client: server.Client()}, lastSuccess: time.Now(), lastPeers: model.Hash("[]")}
	if e = r.Ensure(context.Background()); e != nil || policyCalls.Load() != 0 {
		t.Fatal("duplicate sync", e)
	}
	if e = r.Apply(context.Background()); e == nil || policyCalls.Load() != 1 {
		t.Fatal("periodic drift check was skipped", e)
	}
	_ = db.Update(func(st *model.State) error { st.LastError = ""; return nil })
	changed.Store(true)
	if e = r.Ensure(context.Background()); e == nil || policyCalls.Load() != 2 {
		t.Fatal("new peer was hidden by policy revision cache", e)
	}
}
