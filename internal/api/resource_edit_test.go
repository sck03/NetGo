package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"testing"
)

func TestResourceEditsFenceStaleCreateUpdateAndRecreation(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Server{Store: db, Sessions: &session.Manager{Store: db, Gate: apiGate{}}, Config: site.Config{OverlayCIDR: "100.96.0.0/16", LocalCIDRs: []string{"192.168.18.0/24"}}}
	save := func(r model.Resource, status int) model.Resource {
		t.Helper()
		raw, _ := json.Marshal(r)
		req := httptest.NewRequest("PUT", "/", bytes.NewReader(raw))
		req.SetPathValue("id", r.ID)
		w := httptest.NewRecorder()
		s.putResource(w, req)
		if w.Code != status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if status != 200 {
			return model.Resource{}
		}
		var saved model.Resource
		if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		return saved
	}
	r := model.Resource{ID: "nas", Name: "NAS", CIDR: "192.168.18.20/32", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}
	first := save(r, 200)
	if first.EditToken == "" {
		t.Fatal("missing edit token")
	}
	save(r, 409)
	if got := save(first, 200); got.EditToken != first.EditToken {
		t.Fatal("no-op changed token")
	}
	disabled := first
	disabled.Enabled = false
	disabled = save(disabled, 200)
	stale := first
	stale.Name = "Old editor"
	save(stale, 409)
	st, _ := db.View()
	if st.Resources[r.ID].Enabled {
		t.Fatal("stale editor restored access")
	}
	if err := s.Sessions.MutateResource(context.Background(), r.ID, func(st *model.State) error { delete(st.Resources, r.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	save(disabled, 409)
	created := save(r, 200)
	if created.EditToken == first.EditToken {
		t.Fatal("recreated resource reused token")
	}
	save(first, 409)
}
