package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"netgo/internal/authorization"
	"netgo/internal/model"
	"netgo/internal/site"
	"netgo/internal/store"
	"strings"
	"time"
)

func (s *Service) Create(ctx context.Context, b NewUser) (Created, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var result Created
	b.Name = strings.TrimSpace(b.Name)
	b.Email = strings.TrimSpace(b.Email)
	if b.Name == "" {
		b.Name = b.Alias
	}
	if len(b.Name) > 256 {
		return result, errors.New("invalid display name")
	}
	if !site.ValidID(b.Alias) || !strings.Contains(b.Email, "@") {
		return result, errors.New("invalid alias/email")
	}
	release, ok := s.work.Try("alias:"+strings.ToLower(b.Alias), "email:"+strings.ToLower(b.Email))
	if !ok {
		return result, model.ErrConflict
	}
	defer release()
	state, err := s.Store.AccountCreationView(ctx, b.Alias, b.Email)
	if err != nil {
		return result, err
	}
	if err = authorization.CheckAdmin(ctx, &state); err != nil {
		return result, err
	}
	if err = unique(&state, b.Alias, b.Email); err != nil {
		return result, err
	}
	var upstream struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if s.Management == nil {
		return result, errors.New("management unavailable")
	}
	// Do not claim pre-existing upstream identities. The durable reservation is
	// written before the POST, including when its response never reaches us.
	if id, err := s.findCreated(ctx, b.Email, ""); err != nil {
		return result, err
	} else if id != "" {
		return result, model.ErrConflict
	}
	intent := store.AccountCleanup{ID: model.ID(), Alias: b.Alias, Email: b.Email, Name: b.Name, Pending: true}
	// Another service process must not reconcile a live request. HTTP clients
	// have bounded deadlines; this grace period also covers shutdown and restart.
	intent.CleanupAfter = time.Now().UTC().Add(10 * time.Minute)
	if err := s.Store.ReserveAccountCreation(intent); err != nil {
		if errors.Is(err, model.ErrConflict) {
			return result, errors.New("account_creation_pending")
		}
		return result, err
	}
	if _, err := s.Management.Request(ctx, "POST", "/users", map[string]any{"email": b.Email, "name": b.Name, "role": "user", "auto_groups": []string{}, "is_service_user": false}, &upstream); err != nil {
		var outcome interface{ RequestMayHaveCommitted() bool }
		if errors.As(err, &outcome) && !outcome.RequestMayHaveCommitted() {
			return result, errors.Join(err, s.Store.FinishAccountCleanup(intent.ID))
		}
		var status interface{ HTTPStatus() int }
		if errors.As(err, &status) && status.HTTPStatus() >= 400 && status.HTTPStatus() < 500 && status.HTTPStatus() != http.StatusRequestTimeout {
			return result, errors.Join(err, s.Store.FinishAccountCleanup(intent.ID))
		}
		// The request may still be executing upstream. Never blindly retry POST
		// or forget a result-unknown operation after an empty lookup.
		_ = s.Store.DeferAccountCleanup(intent, time.Now().UTC())
		return result, fmt.Errorf("account_creation_uncertain: %w", err)
	}
	if upstream.ID == "" {
		return result, errors.New("account_creation_uncertain")
	}
	// Retry and creation share ownership of this ID. No network request runs
	// under a database or session lock.
	finish, ok := s.work.Try("created:" + upstream.ID)
	if !ok {
		return result, model.ErrConflict
	}
	defer finish()
	if err := s.Store.ResolveAccountCreation(intent, upstream.ID); err != nil {
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
	u := model.User{ID: upstream.ID, Alias: b.Alias, Name: b.Name, Email: b.Email, Role: "user", MaxDevices: 3, MaxSessions: 1, Resources: []string{}}
	err = s.Store.AccountCreationUpdate(ctx, u, func(st *model.State) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
