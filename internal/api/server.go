package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"netgo/internal/accounts"
	"netgo/internal/devices"
	"netgo/internal/diagnostics"
	"netgo/internal/engine/netbird"
	"netgo/internal/gateway"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/protocol"
	"netgo/internal/remote"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	accountOnce    sync.Once
	accountService *accounts.Service
	Config         site.Config
	Store          *store.Store
	Auth           *identity.Native
	Sessions       *session.Manager
	Management     *netbird.Management
	Remote         *remote.Manager
	deviceOnce     sync.Once
	deviceService  *devices.Service
	Devices        *devices.Service
	publicRates    rateLimiter
	refreshRates   rateLimiter
	Web            http.Handler
	Reconcile      func(context.Context) error
	Monitor        *diagnostics.Monitor
	Logger         *slog.Logger
}
type ctxKey int

const credKey ctxKey = 1
const principalKey ctxKey = 2

type principal struct {
	user   model.User
	device model.Device
}

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
	case errors.Is(e, remote.ErrBusy):
		status = 409
		code = "remote_host_busy"
	case errors.Is(e, remote.ErrUnavailable):
		status = 409
		code = "remote_host_unavailable"
	case errors.Is(e, gateway.ErrCapacity):
		status = 503
		code = "gateway_capacity_exceeded"
	case errors.Is(e, model.ErrConflict):
		status = 409
		code = "session_conflict"
	case errors.Is(e, model.ErrNotFound):
		status = 404
		code = "not_found"
	case e.Error() == "rate_limited":
		status = http.StatusTooManyRequests
		code = "rate_limited"
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
	m.HandleFunc("GET /netgo/v2/health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, protocol.Health{Protocol: model.Protocol, SiteID: s.Config.Site.ID, Capabilities: protocol.Capabilities(), ServerUnix: time.Now().Unix()})
	})
	m.HandleFunc("POST /netgo/v2/auth/transactions", s.anonymous(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	m.HandleFunc("POST /netgo/v2/auth/transactions/{id}/complete", s.anonymous(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	m.HandleFunc("POST /netgo/v2/auth/transactions/{id}/exchange", s.anonymous(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	secured := func(pattern string, admin bool, h http.HandlerFunc) { m.Handle(pattern, s.authorize(admin, h)) }
	s.remoteRoutes(secured)
	secured("GET /netgo/v2/site", false, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, s.Config.Site) })
	secured("POST /netgo/v2/auth/refresh", false, func(w http.ResponseWriter, r *http.Request) {
		c := r.Context().Value(credKey).(model.Credential)
		if !s.refreshRates.allow(c.DeviceID, 12, time.Now()) {
			rateLimited(w)
			return
		}
		var b struct {
			RefreshToken string `json:"refresh_token"`
		}
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Auth.Refresh(r.Context(), c, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), b.RefreshToken)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	secured("GET /netgo/v2/me", false, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, r.Context().Value(principalKey).(principal).user)
	})
	secured("GET /netgo/v2/peer", false, func(w http.ResponseWriter, r *http.Request) {
		c := r.Context().Value(credKey).(model.Credential)
		d := r.Context().Value(principalKey).(principal).device
		p, e := s.Management.FindPeer(r.Context(), c.UserID, d.WireGuardKey)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, p)
	})
	secured("POST /netgo/v2/sessions", false, func(w http.ResponseWriter, r *http.Request) {
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
	secured("POST /netgo/v2/sessions/{id}/renew", false, func(w http.ResponseWriter, r *http.Request) {
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
	secured("DELETE /netgo/v2/sessions/{id}", false, func(w http.ResponseWriter, r *http.Request) {
		c := r.Context().Value(credKey).(model.Credential)
		if e := s.Sessions.Revoke(r.Context(), c.UserID, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"revoked": true})
	})
	secured("POST /netgo/v2/admin/reconcile", true, func(w http.ResponseWriter, r *http.Request) {
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
	secured("GET /netgo/v2/admin/state", true, func(w http.ResponseWriter, r *http.Request) {
		st, e := s.Store.AdminView()
		if e != nil {
			failure(w, e)
			return
		}
		monitor := s.Monitor
		if monitor == nil {
			monitor = diagnostics.NewMonitor()
		}
		reply(w, 200, map[string]any{"users": st.Users, "resources": st.Resources, "devices": st.Devices, "leases": st.Leases, "revision": st.Revision, "observed_revision": st.ObservedRevision, "last_error": st.LastError, "runtime": monitor.Snapshot(s.Config, st), "network": s.Config.Access()})
	})
	secured("GET /netgo/v2/admin/audit", true, func(w http.ResponseWriter, r *http.Request) {
		before := int64(0)
		if value := r.URL.Query().Get("before"); value != "" {
			var e error
			before, e = strconv.ParseInt(value, 10, 64)
			if e != nil || before < 0 {
				failure(w, errors.New("invalid audit cursor"))
				return
			}
		}
		page, e := s.Store.Audit(before, 100)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, page)
	})
	secured("GET /netgo/v2/admin/diagnostics", true, func(w http.ResponseWriter, r *http.Request) {
		state, err := s.Store.AdminView()
		if err != nil {
			failure(w, err)
			return
		}
		monitor := s.Monitor
		if monitor == nil {
			monitor = diagnostics.NewMonitor()
		}
		reply(w, 200, monitor.Snapshot(s.Config, state))
	})
	secured("PUT /netgo/v2/admin/users/{id}", true, s.putUser)
	secured("POST /netgo/v2/admin/users", true, s.createUser)
	secured("DELETE /netgo/v2/admin/users/{id}", true, s.deleteUser)
	secured("DELETE /netgo/v2/admin/devices/{id}", true, s.deleteDevice)
	secured("PUT /netgo/v2/admin/resources/{id}", true, s.putResource)
	secured("DELETE /netgo/v2/admin/resources/{id}", true, func(w http.ResponseWriter, r *http.Request) {
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
	secured("DELETE /netgo/v2/admin/sessions/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		if e := s.Sessions.Revoke(r.Context(), "admin", r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"revoked": true})
	})
	if s.Web != nil {
		m.Handle("/", s.Web)
	}
	return s.observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(protocol.Header, strconv.Itoa(model.Protocol))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.Config.Site.Origin {
			failure(w, model.ErrForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/netgo/") && r.URL.Path != "/netgo/v2/health" {
			versions := r.Header.Values(protocol.Header)
			if len(versions) != 1 || versions[0] != strconv.Itoa(model.Protocol) || !strings.HasPrefix(r.URL.Path, "/netgo/v2/") {
				reply(w, http.StatusUpgradeRequired, map[string]any{"error": "protocol_incompatible", "protocol": model.Protocol, "capabilities": protocol.Capabilities()})
				return
			}
		}
		m.ServeHTTP(w, r)
	}))
}
func (s *Server) authorize(admin bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, u, d, e := s.Store.Identity(model.Hash(token))
		if e != nil {
			failure(w, e)
			return
		}
		if d.DeletePending || d.UserID != c.UserID || !time.Now().Before(c.ExpiresAt) || !u.Active(time.Now()) {
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
		if !identity.Verify(d.PublicKey, identity.SignatureMessage(r.Method, r.URL.RequestURI(), stamp, nonce, b, token), sig) {
			failure(w, model.ErrUnauthorized)
			return
		}
		// Retain through the entire accepted timestamp window, including a stamp
		// 30 seconds in the future. SQLite arbitrates concurrent server processes.
		if e := s.Store.ConsumeNonce(model.Hash("api:"+c.DeviceID+":"+nonce), time.Now(), time.Now().Add(61*time.Second)); e != nil {
			failure(w, model.ErrUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), credKey, c)
		ctx = context.WithValue(ctx, principalKey, principal{u, d})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) accounts() *accounts.Service {
	s.accountOnce.Do(func() {
		s.accountService = &accounts.Service{Store: s.Store, Sessions: s.Sessions, Management: s.Management}
	})
	return s.accountService
}
func (s *Server) putUser(w http.ResponseWriter, r *http.Request) {
	var u model.User
	if e := body(w, r, &u); e != nil {
		failure(w, e)
		return
	}
	if u.ID != r.PathValue("id") {
		failure(w, errors.New("user ID mismatch"))
		return
	}
	u, e := s.accounts().Update(r.Context(), u)
	if e != nil {
		failure(w, e)
		return
	}
	reply(w, 200, u)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var b accounts.NewUser
	if e := body(w, r, &b); e != nil {
		failure(w, e)
		return
	}
	created, e := s.accounts().Create(r.Context(), b)
	if e != nil {
		failure(w, e)
		return
	}
	reply(w, 201, created)
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
	if e := s.Config.ValidateResource(v); e != nil {
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
	if e := s.accounts().Delete(r.Context(), r.PathValue("id")); e != nil {
		failure(w, e)
		return
	}
	reply(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	s.deviceOnce.Do(func() {
		s.deviceService = s.Devices
		if s.deviceService == nil {
			s.deviceService = &devices.Service{Store: s.Store, Sessions: s.Sessions, Management: s.Management}
		}
	})
	result, err := s.deviceService.Delete(r.Context(), r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	status := http.StatusOK
	if result.CleanupPending {
		status = http.StatusAccepted
	}
	reply(w, status, result)
}
