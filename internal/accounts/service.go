// Package accounts coordinates administrative account changes and upstream IO.
package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"strings"
	"sync"
	"time"
)

type Management interface {
	Request(context.Context, string, string, any, any) (http.Header, error)
}
type Service struct {
	mu         sync.Mutex
	Store      *store.Store
	Sessions   *session.Manager
	Management Management
}
type NewUser struct{ Alias, Email, Name string }
type Created struct {
	User     model.User `json:"user"`
	Password string     `json:"initial_password"`
}

func unique(st *model.State, alias, email string) error {
	for _, u := range st.Users {
		if strings.EqualFold(u.Alias, alias) || strings.EqualFold(u.Email, email) {
			return model.ErrConflict
		}
	}
	return nil
}
func (s *Service) Create(ctx context.Context, b NewUser) (Created, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result Created
	if !site.ValidID(b.Alias) || !strings.Contains(b.Email, "@") {
		return result, errors.New("invalid alias/email")
	}
	if e := s.Store.Read(func(st *model.State) error { return unique(st, b.Alias, b.Email) }); e != nil {
		return result, e
	}
	var upstream struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if _, e := s.Management.Request(ctx, "POST", "/users", map[string]any{"email": b.Email, "name": b.Name, "role": "user", "auto_groups": []string{}, "is_service_user": false}, &upstream); e != nil {
		return result, e
	}
	if upstream.ID == "" {
		return result, errors.New("management returned no user ID")
	}
	u := model.User{ID: upstream.ID, Alias: b.Alias, Email: b.Email, Role: "user", MaxDevices: 3, MaxSessions: 1, Resources: []string{}}
	owns := true
	e := s.Store.Update(func(st *model.State) error {
		if _, exists := st.Users[u.ID]; exists {
			owns = false
			return model.ErrConflict
		}
		if e := unique(st, b.Alias, b.Email); e != nil {
			return e
		}
		st.Users[u.ID] = u
		st.Record("admin", "user.create", u.ID)
		return nil
	})
	if e != nil && owns {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.Management.Request(cleanup, "DELETE", "/users/"+url.PathEscape(u.ID), nil, nil); err != nil {
			return result, errors.New("user creation failed; upstream account cleanup required")
		}
	}
	if e != nil {
		return result, e
	}
	return Created{u, upstream.Password}, nil
}
func protectAdmin(st *model.State, u model.User) error {
	if u.Role != "admin" {
		return nil
	}
	for _, other := range st.Users {
		if other.ID != u.ID && other.Role == "admin" && other.Active(time.Now()) {
			return nil
		}
	}
	return errors.New("last administrator cannot be disabled or deleted")
}
func (s *Service) Update(ctx context.Context, u model.User) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u.MaxSessions = u.SessionLimit()
	if !site.ValidID(u.Alias) || (u.Role != "admin" && u.Role != "user") || u.MaxDevices < 1 || u.MaxDevices > 100 || u.MaxSessions < 1 || u.MaxSessions > 100 {
		return u, errors.New("invalid user")
	}
	e := s.Sessions.MutateUser(ctx, u.ID, func(st *model.State) error {
		old, ok := st.Users[u.ID]
		if !ok || old.Email != u.Email {
			return model.ErrNotFound
		}
		for id, v := range st.Users {
			if id != u.ID && strings.EqualFold(v.Alias, u.Alias) {
				return model.ErrConflict
			}
		}
		seen := map[string]bool{}
		resources := make([]string, 0, len(u.Resources))
		for _, id := range u.Resources {
			if _, ok := st.Resources[id]; !ok {
				return errors.New("unknown resource")
			}
			if !seen[id] {
				seen[id] = true
				resources = append(resources, id)
			}
		}
		u.Resources = resources
		if old.Role == "admin" && (u.Role != "admin" || !u.Active(time.Now())) {
			if e := protectAdmin(st, old); e != nil {
				return e
			}
		}
		u.AuthRevision = old.AuthRevision
		st.Users[u.ID] = u
		if e := st.InvalidateUser(u.ID); e != nil {
			return e
		}
		u = st.Users[u.ID]
		st.Record("admin", "user.update", u.ID)
		return nil
	})
	return u, e
}
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Revoke locally first. If upstream is unavailable, a disabled account remains
	// visible and can be retried; no network call holds the database write lock.
	e := s.Sessions.MutateUser(ctx, id, func(st *model.State) error {
		u, ok := st.Users[id]
		if !ok {
			return model.ErrNotFound
		}
		if e := protectAdmin(st, u); e != nil {
			return e
		}
		u.Disabled = true
		st.Users[id] = u
		if e := st.InvalidateUser(id); e != nil {
			return e
		}
		st.Record("admin", "user.delete.pending", id)
		return nil
	})
	if e != nil {
		return e
	}
	if _, e = s.Management.Request(ctx, "DELETE", "/users/"+url.PathEscape(id), nil, nil); e != nil {
		var status interface{ HTTPStatus() int }
		if !errors.As(e, &status) || status.HTTPStatus() != http.StatusNotFound {
			return e
		}
	}

	return s.Sessions.MutateUser(ctx, id, func(st *model.State) error { delete(st.Users, id); st.Record("admin", "user.delete", id); return nil })
}
