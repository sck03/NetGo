package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"netgo/internal/accounts"
	"netgo/internal/devices"
	"netgo/internal/diagnostics"
	"netgo/internal/engine/netbird"
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
	servicesOnce sync.Once
	Accounts     *accounts.Service
	Config       site.Config
	Store        *store.Store
	Auth         *identity.Native
	Sessions     *session.Manager
	Management   *netbird.Management
	Remote       *remote.Manager
	Devices      *devices.Service
	publicRates  rateLimiter
	refreshRates rateLimiter
	Web          http.Handler
	Reconcile    func(context.Context) error
	Monitor      *diagnostics.Monitor
	Logger       *slog.Logger
}

func (s *Server) Handler() http.Handler {
	s.initializeServices()
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
	s.cleanupRoutes(secured)
	secured("GET /netgo/v2/site", false, func(w http.ResponseWriter, r *http.Request) {
		if s.Config.Site.Validate() != nil {
			reply(w, http.StatusServiceUnavailable, map[string]string{"error": "site_not_ready"})
			return
		}
		reply(w, 200, s.Config.Site)
	})
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
	s.adminRoutes(secured)
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
func (s *Server) initializeServices() {
	s.servicesOnce.Do(func() {
		if s.Accounts == nil {
			s.Accounts = &accounts.Service{Store: s.Store, Sessions: s.Sessions, Management: s.Management}
		}
		if s.Devices == nil {
			s.Devices = &devices.Service{Store: s.Store, Sessions: s.Sessions, Management: s.Management}
		}
	})
}
