package api

import (
	"errors"
	"net/http"
	"netgo/internal/authorization"
	"netgo/internal/diagnostics"
	"netgo/internal/model"
	"strconv"
	"time"
)

func (s *Server) adminRoutes(secured func(string, bool, http.HandlerFunc)) {
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
		st, next, e := s.Store.AdminPage(r.Context(), r.URL.Query().Get("section"), r.URL.Query().Get("after"), 50)
		if e != nil {
			failure(w, e)
			return
		}
		monitor := s.Monitor
		if monitor == nil {
			monitor = diagnostics.NewMonitor()
		}
		reply(w, 200, AdminPageState{Users: st.Users, Resources: st.Resources, Devices: st.Devices, Leases: st.Leases, Revision: st.Revision, ObservedRevision: st.ObservedRevision, LastError: st.LastError, Runtime: monitor.Snapshot(s.Config, st), Network: s.Config.Access(), NextAfter: next})
	})
	secured("GET /netgo/v2/admin/resources/choices", true, func(w http.ResponseWriter, r *http.Request) {
		page, err := s.Store.ResourceChoices(r.Context(), r.URL.Query().Get("after"), r.URL.Query().Get("q"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, page)
	})
	secured("GET /netgo/v2/admin/summary", true, func(w http.ResponseWriter, r *http.Request) {
		st, counts, err := s.Store.AdminSummary(r.Context(), time.Now())
		if err != nil {
			failure(w, err)
			return
		}
		monitor := s.Monitor
		if monitor == nil {
			monitor = diagnostics.NewMonitor()
		}
		report := monitor.Snapshot(s.Config, st)
		report.Counts = counts
		reply(w, 200, AdminSummary{Counts: counts, Revision: st.Revision, ObservedRevision: st.ObservedRevision, LastError: st.LastError, Runtime: report})
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
		page, e := s.Store.Audit(r.Context(), before, 100)
		if e != nil {
			failure(w, e)
			return
		}
		reply(w, 200, page)
	})
	secured("GET /netgo/v2/admin/users/choices", true, func(w http.ResponseWriter, r *http.Request) {
		page, err := s.Store.UserChoices(r.Context(), r.URL.Query().Get("after"), r.URL.Query().Get("q"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, page)
	})
	secured("PUT /netgo/v2/admin/users/{id}", true, s.putUser)
	secured("POST /netgo/v2/admin/users", true, s.createUser)
	secured("DELETE /netgo/v2/admin/users/{id}", true, s.deleteUser)
	secured("DELETE /netgo/v2/admin/devices/{id}", true, s.deleteDevice)
	secured("PUT /netgo/v2/admin/resources/{id}", true, s.putResource)
	secured("DELETE /netgo/v2/admin/resources/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		e := s.Sessions.MutateResource(r.Context(), r.PathValue("id"), func(st *model.State) error {
			if err := authorization.CheckAdmin(r.Context(), st); err != nil {
				return err
			}
			delete(st.Resources, r.PathValue("id"))
			st.Record(authorization.Actor(r.Context()), "resource.delete", r.PathValue("id"))
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
}
