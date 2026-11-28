package api

import (
	"errors"
	"net/http"
	"netgo/internal/accounts"
	"netgo/internal/authorization"
	"netgo/internal/model"
	"slices"
)

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
	u, e := s.Accounts.Update(r.Context(), u)
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
	created, e := s.Accounts.Create(r.Context(), b)
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
	v = v.Normalized()
	e := s.Sessions.MutateResource(r.Context(), v.ID, func(st *model.State) error {
		if err := authorization.CheckAdmin(r.Context(), st); err != nil {
			return err
		}
		old, exists := st.Resources[v.ID]
		if err := model.CheckEditToken(old.EditToken, v.EditToken, exists); err != nil {
			return err
		}
		if exists && old.Name == v.Name && old.SameAccess(v) && slices.Equal(old.Ports, v.Ports) {
			return nil
		}
		v.EditToken = model.ID()
		st.Resources[v.ID] = v
		st.Record(authorization.Actor(r.Context()), "resource.update", v.ID)
		return nil
	})
	if e != nil {
		failure(w, e)
		return
	}
	reply(w, 200, v)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	result, e := s.Accounts.Delete(r.Context(), r.PathValue("id"))
	if e != nil {
		failure(w, e)
		return
	}
	status := http.StatusOK
	if result.CleanupPending {
		status = http.StatusAccepted
	}
	reply(w, status, result)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	result, err := s.Devices.Delete(r.Context(), r.PathValue("id"))
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
