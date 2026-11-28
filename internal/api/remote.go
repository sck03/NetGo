package api

import (
	"context"
	"net/http"
	"netgo/internal/model"
	"netgo/internal/remote"
	"strconv"
	"time"
)

func (s *Server) remoteRoutes(secured func(string, bool, http.HandlerFunc)) {
	register := func(pattern string, admin bool, handle http.HandlerFunc) {
		secured(pattern, admin, func(w http.ResponseWriter, r *http.Request) {
			if s.Remote == nil {
				reply(w, http.StatusServiceUnavailable, map[string]string{"error": "remote_unavailable"})
				return
			}
			handle(w, r)
		})
	}
	credential := func(r *http.Request) model.Credential { return r.Context().Value(credKey).(model.Credential) }
	register("GET /netgo/v2/admin/remote/grants", true, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, err := s.Remote.AdminGrants(r.Context(), credential(r), q.Get("host_id"), q.Get("user_id"), q.Get("after"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, page)
	})
	register("GET /netgo/v2/admin/remote/inventory", true, func(w http.ResponseWriter, r *http.Request) {
		page, err := s.Remote.AdminList(r.Context(), credential(r), r.URL.Query().Get("after"), r.URL.Query().Get("q"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, page)
	})
	register("GET /netgo/v2/admin/remote/history", true, func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, err := s.Store.RemoteHistory(r.URL.Query().Get("before"), limit)
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, page)
	})
	register("GET /netgo/v2/remote", false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Remote.List(r.Context(), credential(r), r.URL.Query().Get("after"))
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("PUT /netgo/v2/remote/host", false, func(w http.ResponseWriter, r *http.Request) {
		var b model.Host
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Remote.Register(r.Context(), credential(r), b)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("POST /netgo/v2/remote/host/heartbeat", false, func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Boot string `json:"boot"`
		}
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		if e := s.Remote.Heartbeat(r.Context(), credential(r), b.Boot); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	})
	register("PUT /netgo/v2/admin/remote/grants", true, func(w http.ResponseWriter, r *http.Request) {
		var b model.RemoteGrant
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Remote.Grant(r.Context(), credential(r), b)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("DELETE /netgo/v2/admin/remote/grants/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		if e := s.Remote.DeleteGrant(r.Context(), credential(r), r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"deleted": true})
	})
	register("POST /netgo/v2/remote/sessions", false, func(w http.ResponseWriter, r *http.Request) {
		var b remote.Request
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Remote.Acquire(r.Context(), credential(r), b, r.Header.Get("Idempotency-Key"))
		if e != nil {
			failure(w, e)
			return
		}
		if s.Reconcile == nil {
			s.rollbackRemote(r, v.Session.ID)
			failure(w, remote.ErrUnavailable)
			return
		}
		if e = s.Reconcile(r.Context()); e != nil {
			s.rollbackRemote(r, v.Session.ID)
			failure(w, remote.ErrUnavailable)
			return
		}
		reply(w, 201, v)
	})
	register("POST /netgo/v2/remote/sessions/{id}/consume", false, func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Sequence uint64            `json:"sequence"`
			Boot     string            `json:"boot"`
			Proof    remote.Possession `json:"proof"`
		}
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Remote.Consume(r.Context(), credential(r), r.PathValue("id"), b.Sequence, b.Boot, b.Proof)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("GET /netgo/v2/remote/sessions/{id}", false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Remote.Inspect(r.Context(), credential(r), r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("POST /netgo/v2/remote/sessions/{id}/renew", false, func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Sequence uint64 `json:"sequence"`
		}
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Remote.Renew(r.Context(), credential(r), r.PathValue("id"), b.Sequence)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("DELETE /netgo/v2/remote/sessions/{id}", false, func(w http.ResponseWriter, r *http.Request) {
		if e := s.Remote.Revoke(r.Context(), credential(r), r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"revoked": true})
	})
}

// Compensation has its own bounded lifetime after the request is cancelled.
func (s *Server) rollbackRemote(r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Remote.Revoke(ctx, r.Context().Value(credKey).(model.Credential), id)
}
