package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"netgo/internal/engine/netbird"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/protocol"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"strconv"
	"testing"
	"time"
)

type apiGate struct{}

func (apiGate) Apply(context.Context, []model.Lease) error { return nil }

func TestUserDeletionRoleAndLastAdministrator(t *testing.T) {
	for _, role := range []string{"user", "admin"} {
		t.Run(role, func(t *testing.T) {
			db, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			token := model.ID()
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
			t.Cleanup(upstream.Close)
			_ = db.Update(func(st *model.State) error {
				st.Users["u"] = model.User{ID: "u", Role: role}
				st.Devices["d"] = model.Device{ID: "d", UserID: "u", PublicKey: base64.RawURLEncoding.EncodeToString(public)}
				st.Credentials[model.Hash(token)] = model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
				return nil
			})
			service := Server{Store: db, Sessions: &session.Manager{Store: db, Gate: apiGate{}}, Management: &netbird.Management{URL: upstream.URL, Client: upstream.Client()}, Config: site.Config{Site: site.Profile{Origin: "https://site.test"}}}
			path := "/netgo/v2/admin/users/u"
			request := httptest.NewRequest("DELETE", path, nil)
			request.Header.Set(protocol.Header, "2")
			stamp := strconv.FormatInt(time.Now().Unix(), 10)
			nonce := model.ID()
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("X-NetGo-Time", stamp)
			request.Header.Set("X-NetGo-Nonce", nonce)
			request.Header.Set("X-NetGo-Signature", base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(identity.SignatureMessage("DELETE", path, stamp, nonce, nil, token)))))
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			if response.Code >= 200 && response.Code < 300 {
				t.Fatal("protected user was deleted")
			}
			if role == "user" && response.Code != 403 {
				t.Fatal("ordinary user reached deletion", response.Code)
			}
			if calls != 0 {
				t.Fatal("upstream deletion was attempted before authorization checks")
			}
			state, err := db.View()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := state.Users["u"]; !ok {
				t.Fatal("user metadata was deleted")
			}
		})
	}
}

func TestSignedAPIReplayAndRoleBoundary(t *testing.T) {
	s, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	pub, private, _ := ed25519.GenerateKey(rand.Reader)
	token := model.ID()
	_ = s.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Role: "user"}
		st.Devices["d"] = model.Device{ID: "d", UserID: "u", PublicKey: base64.RawURLEncoding.EncodeToString(pub)}
		st.Credentials[model.Hash(token)] = model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
		return nil
	})
	server := Server{Store: s, Config: site.Config{Site: site.Profile{ID: "site1", Origin: "https://site.test"}}}
	handler := server.Handler()
	makeReq := func(path, nonce string) *http.Request {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set(protocol.Header, "2")
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-NetGo-Time", stamp)
		r.Header.Set("X-NetGo-Nonce", nonce)
		r.Header.Set("X-NetGo-Signature", base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(identity.SignatureMessage("GET", path, stamp, nonce, nil, token)))))
		return r
	}
	nonce := model.ID()
	r := makeReq("/netgo/v2/me", nonce)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, makeReq("/netgo/v2/me", nonce))
	if w.Code != 401 {
		t.Fatal("replay accepted", w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, makeReq("/netgo/v2/admin/state", model.ID()))
	if w.Code != 403 {
		t.Fatal("ordinary user reached admin", w.Code)
	}
}
func TestHealthMinimalAndCrossOriginBlocked(t *testing.T) {
	s := Server{Config: site.Config{Site: site.Profile{ID: "site1", Origin: "https://site.test"}}}
	h := s.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/netgo/v2/health", nil))
	var health protocol.Health
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil || health.Validate("site1") != nil {
		t.Fatal(w.Body.String())
	}
	var fields map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &fields)
	if len(fields) != 4 || health.ServerUnix <= 0 || time.Since(time.Unix(health.ServerUnix, 0)) > 2*time.Second {
		t.Fatal("public health disclosed extra fields")
	}
	r := httptest.NewRequest("GET", "/netgo/v2/health", nil)
	r.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted")
	}
}
