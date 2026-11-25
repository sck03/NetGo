package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"testing"
)

func TestInvalidResourceEditDoesNotRevokeExistingAccess(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	original := model.Resource{ID: "files", Name: "Files", CIDR: "192.168.18.20/32", Protocol: "tcp", Ports: []uint16{445}, Enabled: true}
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Alias: "test", Role: "user"}
		st.Devices["d"] = model.Device{ID: "d", UserID: "u"}
		st.Resources[original.ID] = original
		st.Leases["lease"] = model.Lease{ID: "lease", UserID: "u", DeviceID: "d", Resources: []model.Resource{original}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := Server{Store: db, Sessions: &session.Manager{Store: db, Gate: apiGate{}}, Config: site.Config{OverlayCIDR: "100.96.0.0/16", LocalCIDRs: []string{"192.168.18.0/23"}}}
	invalid := original
	invalid.CIDR = "8.8.8.8/32"
	raw, _ := json.Marshal(invalid)
	r := httptest.NewRequest("PUT", "/", bytes.NewReader(raw))
	r.SetPathValue("id", original.ID)
	w := httptest.NewRecorder()
	s.putResource(w, r)
	if w.Code != 400 || bytes.Contains(w.Body.Bytes(), []byte("operation_failed")) {
		t.Fatal(w.Code, w.Body.String())
	}
	st, err := db.View()
	if err != nil {
		t.Fatal(err)
	}
	if st.Resources[original.ID].CIDR != original.CIDR || len(st.Leases) != 1 {
		t.Fatal("rejected edit changed active access")
	}
}
