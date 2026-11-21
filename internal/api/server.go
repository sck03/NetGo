package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"netgo/internal/engine/netbird"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Config       site.Config
	Store        *store.Store
	Auth         *identity.Native
	Sessions     *session.Manager
	Management   *netbird.Management
	mu           sync.Mutex
	nonces       map[string]time.Time
	publicCount  int
	publicWindow time.Time
	Web          http.Handler
	Reconcile    func(context.Context) error
}
type ctxKey int

const credKey ctxKey = 1

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, e error) {
	status := 400
	code := "invalid_request"
	switch {
	case errors.Is(e, model.ErrUnauthorized):
		status = 401
		code = "unauthorized"
	case errors.Is(e, model.ErrForbidden):
		status = 403
		code = "forbidden"
	case errors.Is(e, model.ErrConflict):
		status = 409
		code = "session_conflict"
	case errors.Is(e, model.ErrNotFound):
		status = 404
		code = "not_found"
	default:
		code = e.Error()
		if len(code) > 160 {
			code = "operation_failed"
		}
	}
	reply(w, status, map[string]string{"error": code})
}
func body(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	return site.Decode(r.Body, v)
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /netgo/v1/health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"protocol": 1, "site_id": s.Config.Site.ID})
	})
	m.HandleFunc("POST /netgo/v1/auth/transactions", func(w http.ResponseWriter, r *http.Request) {
		var b identity.Begin
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		t, e := s.Auth.Begin(b)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 201, t)
	})
	m.HandleFunc("POST /netgo/v1/auth/transactions/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Alias, Password, Signature string }
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		if e := s.Auth.Complete(r.Context(), r.PathValue("id"), b.Alias, b.Password, b.Signature); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"authorized": true})
	})
	m.HandleFunc("POST /netgo/v1/auth/transactions/{id}/exchange", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Verifier, Signature string }
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Auth.Exchange(r.Context(), r.PathValue("id"), b.Verifier, b.Signature)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	secured := func(pattern string, admin bool, h http.HandlerFunc) { m.Handle(pattern, s.authorize(admin, h)) }
	secured("GET /netgo/v1/site", false, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, s.Config.Site) })
	secured("POST /netgo/v1/auth/refresh", false, func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			RefreshToken string `json:"refresh_token"`
		}
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		c := r.Context().Value(credKey).(model.Credential)
		v, e := s.Auth.Refresh(r.Context(), c, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), b.RefreshToken)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	secured("GET /netgo/v1/me", false, func(w http.ResponseWriter, r *http.Request) {
		st, e := s.Store.View()
		if e != nil {
			failure(w, e)
			return
		}
		c := r.Context().Value(credKey).(model.Credential)
		reply(w, 200, st.Users[c.UserID])
	})
	secured("GET /netgo/v1/peer", false, func(w http.ResponseWriter, r *http.Request) {
		c := r.Context().Value(credKey).(model.Credential)
		st, e := s.Store.View()
		if e != nil {
			failure(w, e)
			return
		}
		p, e := s.Management.FindPeer(r.Context(), c.UserID, st.Devices[c.DeviceID].WireGuardKey)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, p)
	})
	secured("POST /netgo/v1/sessions", false, func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			PeerID     string `json:"peer_id"`
			Generation uint64 `json:"generation"`
			Replace    bool   `json:"replace"`
		}
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		if s.Reconcile == nil {
			failure(w, errors.New("network coordinator unavailable"))
			return
		}
		if e := s.Reconcile(r.Context()); e != nil {
			failure(w, e)
			return
		}
		l, e := s.Sessions.Acquire(r.Context(), r.Context().Value(credKey).(model.Credential), b.PeerID, b.Generation, b.Replace, r.Header.Get("Idempotency-Key"))
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 201, l)
	})
	secured("POST /netgo/v1/sessions/{id}/renew", false, func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Generation uint64 }
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		l, e := s.Sessions.Renew(r.Context(), r.Context().Value(credKey).(model.Credential), r.PathValue("id"), b.Generation)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, l)
	})
	secured("DELETE /netgo/v1/sessions/{id}", false, func(w http.ResponseWriter, r *http.Request) {
		c := r.Context().Value(credKey).(model.Credential)
		if e := s.Sessions.Revoke(r.Context(), c.UserID, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"revoked": true})
	})
	secured("POST /netgo/v1/admin/reconcile", true, func(w http.ResponseWriter, r *http.Request) {
		if s.Reconcile == nil {
			failure(w, errors.New("coordinator unavailable"))
			return
		}
		if e := s.Reconcile(r.Context()); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"applied": true})
	})
	secured("GET /netgo/v1/admin/state", true, func(w http.ResponseWriter, r *http.Request) {
		st, e := s.Store.View()
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]any{"users": st.Users, "resources": st.Resources, "devices": st.Devices, "leases": st.Leases, "audit": st.Audit, "revision": st.Revision, "observed_revision": st.ObservedRevision, "last_error": st.LastError})
	})
	secured("PUT /netgo/v1/admin/users/{id}", true, s.putUser)
	secured("POST /netgo/v1/admin/users", true, s.createUser)
	secured("DELETE /netgo/v1/admin/users/{id}", true, s.deleteUser)
	secured("DELETE /netgo/v1/admin/devices/{id}", true, s.deleteDevice)
	secured("PUT /netgo/v1/admin/resources/{id}", true, s.putResource)
	secured("DELETE /netgo/v1/admin/resources/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		e := s.Sessions.MutateResource(r.Context(), r.PathValue("id"), func(st *model.State) error {
			delete(st.Resources, r.PathValue("id"))
			st.Record("admin", "resource.delete", r.PathValue("id"))
			return nil
		})
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"deleted": true})
	})
	secured("DELETE /netgo/v1/admin/sessions/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		if e := s.Sessions.Revoke(r.Context(), "admin", r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"revoked": true})
	})
	if s.Web != nil {
		m.Handle("/", s.Web)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.Config.Site.Origin {
			failure(w, model.ErrForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/netgo/v1/auth/") {
			s.mu.Lock()
			if time.Since(s.publicWindow) > time.Minute {
				s.publicWindow = time.Now()
				s.publicCount = 0
			}
			s.publicCount++
			limited := s.publicCount > 300
			s.mu.Unlock()
			if limited {
				reply(w, 429, map[string]string{"error": "rate_limited"})
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Server) authorize(admin bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		st, e := s.Store.View()
		if e != nil {
			failure(w, e)
			return
		}
		c, ok := st.Credentials[model.Hash(token)]
		u := st.Users[c.UserID]
		if !ok || !time.Now().Before(c.ExpiresAt) || !u.Active(time.Now()) {
			failure(w, model.ErrUnauthorized)
			return
		}
		if admin && u.Role != "admin" {
			failure(w, model.ErrForbidden)
			return
		}
		stamp, nonce, sig := r.Header.Get("X-NetGo-Time"), r.Header.Get("X-NetGo-Nonce"), r.Header.Get("X-NetGo-Signature")
		ts, e := strconv.ParseInt(stamp, 10, 64)
		if e != nil || time.Since(time.Unix(ts, 0)) > 30*time.Second || time.Until(time.Unix(ts, 0)) > 30*time.Second || len(nonce) < 32 || len(nonce) > 128 {
			failure(w, model.ErrUnauthorized)
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if e != nil {
			failure(w, e)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		if !identity.Verify(st.Devices[c.DeviceID].PublicKey, identity.SignatureMessage(r.Method, r.URL.RequestURI(), stamp, nonce, b, token), sig) {
			failure(w, model.ErrUnauthorized)
			return
		}
		s.mu.Lock()
		if s.nonces == nil {
			s.nonces = map[string]time.Time{}
		}
		for k, t := range s.nonces {
			if time.Since(t) > time.Minute {
				delete(s.nonces, k)
			}
		}
		key := model.Hash(token + nonce)
		_, used := s.nonces[key]
		if !used && len(s.nonces) < 65536 {
			s.nonces[key] = time.Now()
		} else {
			used = true
		}
		s.mu.Unlock()
		if used {
			failure(w, model.ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), credKey, c)))
	})
}
func (s *Server) putUser(w http.ResponseWriter, r *http.Request) {
	var u model.User
	if e := body(w, r, &u); e != nil {
		failure(w, e)
		return
	}
	if u.MaxSessions == 0 {
		u.MaxSessions = 1
	}
	if u.ID != r.PathValue("id") || !site.ValidID(u.Alias) || (u.Role != "admin" && u.Role != "user") || u.MaxDevices < 1 || u.MaxDevices > 100 || u.MaxSessions < 1 || u.MaxSessions > 100 {
		failure(w, errors.New("invalid user"))
		return
	}
	e := s.Sessions.MutateUser(r.Context(), u.ID, func(st *model.State) error {
		old, ok := st.Users[u.ID]
		if !ok || u.Email != old.Email {
			return model.ErrNotFound
		}
		for id, v := range st.Users {
			if id != u.ID && strings.EqualFold(v.Alias, u.Alias) {
				return model.ErrConflict
			}
		}
		for _, id := range u.Resources {
			if _, ok := st.Resources[id]; !ok {
				return errors.New("unknown resource")
			}
		}
		if old.Role == "admin" && (u.Role != "admin" || !u.Active(time.Now())) {
			count := 0
			for _, v := range st.Users {
				if v.ID != u.ID && v.Role == "admin" && v.Active(time.Now()) {
					count++
				}
			}
			if count == 0 {
				return errors.New("last administrator cannot be disabled")
			}
		}
		st.Users[u.ID] = u
		for k, c := range st.Credentials {
			if c.UserID == u.ID {
				delete(st.Credentials, k)
			}
		}
		st.Record("admin", "user.update", u.ID)
		return nil
	})
	if e != nil {
		failure(w, e)
		return
	}
	reply(w, 200, u)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var b struct{ Alias, Email, Name string }
	if e := body(w, r, &b); e != nil {
		failure(w, e)
		return
	}
	if !site.ValidID(b.Alias) || !strings.Contains(b.Email, "@") {
		failure(w, errors.New("invalid alias/email"))
		return
	}
	st, e := s.Store.View()
	if e != nil {
		failure(w, e)
		return
	}
	for _, u := range st.Users {
		if strings.EqualFold(u.Alias, b.Alias) || strings.EqualFold(u.Email, b.Email) {
			failure(w, model.ErrConflict)
			return
		}
	}
	var upstream struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	_, e = s.Management.Request(r.Context(), "POST", "/users", map[string]any{"email": b.Email, "name": b.Name, "role": "user", "auto_groups": []string{}, "is_service_user": false}, &upstream)
	if e != nil {
		failure(w, e)
		return
	}
	u := model.User{ID: upstream.ID, Alias: b.Alias, Email: b.Email, Role: "user", MaxDevices: 3, MaxSessions: 1, Resources: []string{}}
	e = s.Store.Update(func(st *model.State) error { st.Users[u.ID] = u; st.Record("admin", "user.create", u.ID); return nil })
	if e != nil {
		failure(w, e)
		return
	}
	reply(w, 201, map[string]any{"user": u, "initial_password": upstream.Password})
}
func (s *Server) putResource(w http.ResponseWriter, r *http.Request) {
	var v model.Resource
	if e := body(w, r, &v); e != nil {
		failure(w, e)
		return
	}
	if v.ID != r.PathValue("id") {
		failure(w, errors.New("resource ID mismatch"))
		return
	}
	if e := site.ValidateResource(v, s.Config.OverlayCIDR); e != nil {
		failure(w, e)
		return
	}
	e := s.Sessions.MutateResource(r.Context(), v.ID, func(st *model.State) error {
		st.Resources[v.ID] = v
		st.Record("admin", "resource.update", v.ID)
		return nil
	})
	if e != nil {
		failure(w, e)
		return
	}
	reply(w, 200, v)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Sessions.MutateUser(r.Context(), id, func(st *model.State) error {
		user, ok := st.Users[id]
		if !ok {
			return model.ErrNotFound
		}
		if user.Role == "admin" {
			another := false
			for _, candidate := range st.Users {
				if candidate.ID != id && candidate.Role == "admin" && candidate.Active(time.Now()) {
					another = true
					break
				}
			}
			if !another {
				return errors.New("last administrator cannot be deleted")
			}
		}
		if _, err := s.Management.Request(r.Context(), "DELETE", "/users/"+url.PathEscape(id), nil, nil); err != nil {
			return err
		}
		delete(st.Users, id)
		for key, credential := range st.Credentials {
			if credential.UserID == id {
				delete(st.Credentials, key)
			}
		}
		for key, device := range st.Devices {
			if device.UserID == id {
				delete(st.Devices, key)
			}
		}
		st.Record("admin", "user.delete", id)
		return nil
	})
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.Store.View()
	if err != nil {
		failure(w, err)
		return
	}
	device, ok := st.Devices[id]
	if !ok {
		failure(w, model.ErrNotFound)
		return
	}
	if device.PeerID != "" {
		if _, err = s.Management.Request(r.Context(), "DELETE", "/peers/"+url.PathEscape(device.PeerID), nil, nil); err != nil {
			failure(w, err)
			return
		}
	}
	err = s.Sessions.MutateDevice(r.Context(), id, func(state *model.State) error {
		delete(state.Devices, id)
		for key, c := range state.Credentials {
			if c.DeviceID == id {
				delete(state.Credentials, key)
			}
		}
		state.Record("admin", "device.delete", id)
		return nil
	})
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 200, map[string]bool{"deleted": true})
}
