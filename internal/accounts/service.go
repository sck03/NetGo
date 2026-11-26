// Package accounts coordinates administrative account changes and upstream IO.
package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"netgo/internal/authorization"
	"netgo/internal/coordination"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"strings"
	"time"
)

type Management interface {
	Request(context.Context, string, string, any, any) (http.Header, error)
}
type Service struct {
	work       coordination.InFlight
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
	u.Name = strings.TrimSpace(u.Name)
	if u.Name == "" {
		u.Name = u.Alias
	}
	if len(u.Name) > 256 {
		return u, errors.New("invalid display name")
	}
	if !site.ValidID(u.Alias) || (u.Role != "admin" && u.Role != "user") || u.MaxDevices < 1 || u.MaxDevices > 100 || u.MaxSessions < 1 || u.MaxSessions > 100 {
		return u, errors.New("invalid user")
	}
	e := s.Sessions.MutateUser(ctx, u.ID, func(st *model.State) error {
		if err := authorization.CheckAdmin(ctx, st); err != nil {
			return err
		}
		old, ok := st.Users[u.ID]
		if !ok || old.Email != u.Email {
			return model.ErrNotFound
		}
		// Login names key the protected device identity on every native client.
		// Display names may change; login names are immutable.
		if u.Alias != old.Alias {
			return errors.New("alias_immutable")
		}
		if old.DeletePending || u.DeletePending {
			return model.ErrConflict
		}
		u.CleanupRetry = old.CleanupRetry
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
		st.Record(authorization.Actor(ctx), "user.update", u.ID)
		return nil
	})
	return u, e
}
func (s *Service) Delete(ctx context.Context, id string) (model.DeleteResult, error) {
	var exists bool
	err := s.Sessions.MutateUser(ctx, id, func(st *model.State) error {
		if err := authorization.CheckAdmin(ctx, st); err != nil {
			return err
		}
		u, ok := st.Users[id]
		exists = ok
		if !ok || u.DeletePending {
			return nil
		}
		if err := protectAdmin(st, u); err != nil {
			return err
		}
		u.Disabled = true
		u.DeletePending = true
		st.Users[id] = u
		if err := st.InvalidateUser(id); err != nil {
			return err
		}
		st.Record(authorization.Actor(ctx), "user.delete.pending", id)
		return nil
	})
	if err != nil {
		return model.DeleteResult{}, err
	}
	if !exists {
		return model.DeleteResult{Deleted: true}, nil
	}
	if err = s.cleanup(ctx, id); err != nil {
		return model.DeleteResult{CleanupPending: true}, nil
	}
	return model.DeleteResult{Deleted: true}, nil
}

func (s *Service) cleanup(ctx context.Context, id string) error {
	release, ok := s.work.Try("cleanup:" + id)
	if !ok {
		return coordination.ErrBusy
	}
	defer release()
	u, err := s.Store.User(id)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !u.DeletePending {
		return nil
	}
	attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.Management == nil {
		err = errors.New("management unavailable")
	} else {
		_, err = s.Management.Request(attempt, "DELETE", "/users/"+url.PathEscape(id), nil, nil)
		var status interface{ HTTPStatus() int }
		if errors.As(err, &status) && status.HTTPStatus() == http.StatusNotFound {
			err = nil
		}
	}
	if err == nil {
		err = s.Sessions.MutateUser(ctx, id, func(st *model.State) error {
			if u, ok := st.Users[id]; ok && u.DeletePending {
				delete(st.Users, id)
				st.Record("system", "user.delete", id)
			}
			return nil
		})
	}
	if err == nil {
		return nil
	}
	return errors.Join(err, s.Store.Update(func(st *model.State) error {
		if u, ok := st.Users[id]; ok && u.DeletePending {
			u.CleanupRetry.Failed(time.Now().UTC())
			st.Users[id] = u
		}
		return nil
	}))
}

func (s *Service) Retry(ctx context.Context) error {
	creationErr := s.retryCreated(ctx)
	items, err := s.Store.PendingUsers(time.Now(), 16)
	if err != nil {
		return errors.Join(creationErr, err)
	}
	err = creationErr
	for _, u := range items {
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		if next := s.cleanup(ctx, u.ID); !errors.Is(next, coordination.ErrBusy) {
			err = errors.Join(err, next)
		}
	}
	return err
}
