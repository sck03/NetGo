package api

import "net/http"

func (s *Server) cleanupRoutes(secured func(string, bool, http.HandlerFunc)) {
	secured("GET /netgo/v2/admin/account-creations", true, func(w http.ResponseWriter, r *http.Request) {
		page, err := s.Store.AccountCleanups(r.Context(), r.URL.Query().Get("after"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, page)
	})
	secured("POST /netgo/v2/admin/account-creations/{id}", true, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ConfirmedAbsent bool `json:"confirmed_absent"`
		}
		if err := body(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if err := s.Accounts.ManageCreation(r.Context(), r.PathValue("id"), request.ConfirmedAbsent); err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, map[string]bool{"checked": true})
	})
}
