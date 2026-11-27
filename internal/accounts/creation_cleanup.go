package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"netgo/internal/model"
	"netgo/internal/store"
	"strings"
	"time"
)

func (s *Service) findCreated(ctx context.Context, email, name string) (string, error) {
	if s.Management == nil {
		return "", errors.New("management unavailable")
	}
	var users []struct{ ID, Email, Name string }
	if _, err := s.Management.Request(ctx, "GET", "/users", nil, &users); err != nil {
		return "", err
	}
	var id string
	for _, u := range users {
		if !strings.EqualFold(u.Email, email) {
			continue
		}
		if u.ID == "" || id != "" || name != "" && u.Name != name {
			return "", model.ErrConflict
		}
		id = u.ID
	}
	return id, nil
}

func (s *Service) recoverCreation(ctx context.Context, item store.AccountCleanup) error {
	if !item.Pending {
		return s.cleanupCreated(ctx, item.ID)
	}
	attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	id, err := s.findCreated(attempt, item.Email, item.Name)
	if err != nil || id == "" {
		return errors.Join(err, s.Store.DeferAccountCleanup(item, time.Now().UTC()))
	}
	if err = s.Store.ResolveAccountCreation(item, id); err != nil {
		return err
	}
	return s.cleanupCreated(ctx, id)
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
		if release, ok := s.work.Try("created:"+item.ID, "alias:"+strings.ToLower(item.Alias), "email:"+strings.ToLower(item.Email)); ok {
			err = errors.Join(err, s.recoverCreation(ctx, item))
			release()
		}
	}
	return err
}
