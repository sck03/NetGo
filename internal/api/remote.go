package api

import (
	"net/http"
	"netgo/internal/model"
	"netgo/internal/remote"
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
	register("GET /netgo/v2/remote", false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Remote.List(credential(r))
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("GET /netgo/v2/remote/key", false, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]string{"site_id": s.Config.Site.ID, "public_key": s.Remote.PublicKey()})
	})
	register("PUT /netgo/v2/remote/host", false, func(w http.ResponseWriter, r *http.Request) {
		var b model.Host
		if e := body(w, r, &b); e != nil {
			failure(w, e)
			return
		}
		v, e := s.Remote.Register(credential(r), b)
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
		if e := s.Remote.Heartbeat(credential(r), b.Boot); e != nil {
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
		v, e := s.Remote.Grant(credential(r), b)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("DELETE /netgo/v2/admin/remote/grants/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		if e := s.Remote.DeleteGrant(credential(r), r.PathValue("id")); e != nil {
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
			_ = s.Remote.Revoke(credential(r), v.Session.ID)
			failure(w, remote.ErrUnavailable)
			return
		}
		if e = s.Reconcile(r.Context()); e != nil {
			_ = s.Remote.Revoke(credential(r), v.Session.ID)
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
		v, e := s.Remote.Consume(credential(r), r.PathValue("id"), b.Sequence, b.Boot, b.Proof)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, v)
	})
	register("GET /netgo/v2/remote/sessions/{id}", false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Remote.Inspect(credential(r), r.PathValue("id"))
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
		if e := s.Remote.Revoke(credential(r), r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, map[string]bool{"revoked": true})
	})
}
