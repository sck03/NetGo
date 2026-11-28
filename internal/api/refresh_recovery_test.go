package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/store"
	"strconv"
	"testing"
	"time"
)

func TestPreviousTokenOnlyAuthorizesSignedRefresh(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pub, private, _ := ed25519.GenerateKey(rand.Reader)
	old, current := "old", "current"
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u"}
		st.Devices["d"] = model.Device{ID: "d", UserID: "u", PublicKey: base64.RawURLEncoding.EncodeToString(pub)}
		st.Credentials[model.Hash(current)] = model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour), Recovery: &model.RefreshRecovery{PreviousHash: model.Hash(old), Until: time.Now().Add(time.Minute)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := Server{Store: db}
	handler := s.authorize(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	request := func(method, path, nonce string, sign bool) int {
		r := httptest.NewRequest(method, path, nil)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("Authorization", "Bearer "+old)
		r.Header.Set("X-NetGo-Time", stamp)
		r.Header.Set("X-NetGo-Nonce", nonce)
		if sign {
			r.Header.Set("X-NetGo-Signature", base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(identity.SignatureMessage(method, path, stamp, nonce, nil, old)))))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	nonce := model.ID()
	if code := request("POST", "/netgo/v2/auth/refresh", nonce, true); code != 204 {
		t.Fatal(code)
	}
	if code := request("POST", "/netgo/v2/auth/refresh", nonce, true); code != 401 {
		t.Fatal("reused signature accepted", code)
	}
	if code := request("POST", "/netgo/v2/auth/refresh", model.ID(), false); code != 401 {
		t.Fatal("unsigned recovery", code)
	}
	if code := request("GET", "/netgo/v2/site", model.ID(), true); code != 401 {
		t.Fatal("previous token reached business API", code)
	}
	if err = db.Update(func(st *model.State) error {
		c := st.Credentials[model.Hash(current)]
		c.Recovery.Until = time.Now().Add(-time.Second)
		st.Credentials[model.Hash(current)] = c
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = db.RefreshIdentity(context.Background(), model.Hash(old)); err == nil {
		t.Fatal("expired recovery retained")
	}
	if code := request("POST", "/netgo/v2/auth/refresh", model.ID(), true); code != 401 {
		t.Fatal("expired recovery accepted", code)
	}
	w := httptest.NewRecorder()
	failure(w, model.ControlFailure(model.ErrControlCapacity))
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("capacity must not revoke login", w.Code)
	}
}
