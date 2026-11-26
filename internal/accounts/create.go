package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"netgo/internal/authorization"
	"netgo/internal/model"
	"netgo/internal/site"
	"strings"
	"time"
)

func (s *Service) Create(ctx context.Context, b NewUser) (Created, error) {
	var result Created
	if !site.ValidID(b.Alias) || !strings.Contains(b.Email, "@") {
		return result, errors.New("invalid alias/email")
	}
	release, ok := s.work.Try("alias:"+strings.ToLower(b.Alias), "email:"+strings.ToLower(b.Email))
	if !ok {
		return result, model.ErrConflict
	}
	defer release()
	if err := s.Store.Read(func(st *model.State) error {
		if err := authorization.CheckAdmin(ctx, st); err != nil {
			return err
		}
		return unique(st, b.Alias, b.Email)
	}); err != nil {
		return result, err
	}
	var upstream struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if s.Management == nil {
		return result, errors.New("management unavailable")
	}
	if _, err := s.Management.Request(ctx, "POST", "/users", map[string]any{"email": b.Email, "name": b.Name, "role": "user", "auto_groups": []string{}, "is_service_user": false}, &upstream); err != nil {
		return result, err
	}
	if upstream.ID == "" {
		return result, errors.New("management returned no user ID")
	}
	// Retry and creation share ownership of this ID. No network request runs
	// under a database or session lock.
	finish, ok := s.work.Try("created:" + upstream.ID)
	if !ok {
		return result, model.ErrConflict
	}
	defer finish()
	if err := s.Store.QueueAccountCleanup(upstream.ID); err != nil {
		if errors.Is(err, model.ErrConflict) {
			return result, err
		}
		if _, lookupErr := s.Store.User(upstream.ID); !errors.Is(lookupErr, model.ErrNotFound) {
			return result, errors.Join(err, lookupErr)
		}
		// Storage failure cannot create a durable job. Attempt immediate cleanup
		// and preserve both errors so the caller can report the failed operation.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return result, errors.Join(err, s.deleteCreated(cleanup, upstream.ID))
	}
	u := model.User{ID: upstream.ID, Alias: b.Alias, Email: b.Email, Role: "user", MaxDevices: 3, MaxSessions: 1, Resources: []string{}}
	err := s.Store.Update(func(st *model.State) error {
		if _, exists := st.Users[u.ID]; exists {
			return model.ErrConflict
		}
		if err := authorization.CheckAdmin(ctx, st); err != nil {
			return err
		}
		if err := unique(st, b.Alias, b.Email); err != nil {
			return err
		}
		st.Users[u.ID] = u
		st.Record(authorization.Actor(ctx), "user.create", u.ID)
		return nil
	})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return result, errors.Join(err, s.cleanupCreated(cleanup, u.ID))
	}
	// If removing the job fails, Retry recognizes the committed user and only
	// removes the job. A successful creation must still return its password.
	_ = s.Store.FinishAccountCleanup(u.ID)
	return Created{u, upstream.Password}, nil
}

func (s *Service) deleteCreated(ctx context.Context, id string) error {
	if s.Management == nil {
		return errors.New("management unavailable")
	}
	_, err := s.Management.Request(ctx, "DELETE", "/users/"+url.PathEscape(id), nil, nil)
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) && status.HTTPStatus() == http.StatusNotFound {
		return nil
	}
	return err
}

// Called with the per-ID creation lock held. Check local ownership even after a
// restart: a crash may leave a cleanup job beside a successfully committed user.
func (s *Service) cleanupCreated(ctx context.Context, id string) error {
	item, err := s.Store.AccountCleanup(id)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.Store.User(id)
	if err == nil {
		return s.Store.FinishAccountCleanup(id)
	}
	if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = s.deleteCreated(attempt, id); err == nil {
		return s.Store.FinishAccountCleanup(id)
	}
	return errors.Join(err, s.Store.DeferAccountCleanup(item, time.Now().UTC()))
}

func (s *Service) retryCreated(ctx context.Context) error {
	items, err := s.Store.PendingAccountCleanups(time.Now(), 16)
	if err != nil {
		return err
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		if release, ok := s.work.Try("created:" + item.ID); ok {
			err = errors.Join(err, s.cleanupCreated(ctx, item.ID))
			release()
		}
	}
	return err
}
