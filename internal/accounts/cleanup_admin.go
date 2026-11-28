package accounts

import (
	"context"
	"errors"
	"netgo/internal/authorization"
	"netgo/internal/model"
	"strings"
	"time"
)

// Empty lookups cannot prove a timed-out POST will never commit. Resolution
// requires an operator to confirm upstream completion, then checks absence again.
func (s *Service) ManageCreation(ctx context.Context, id string, confirmedAbsent bool) error {
	item, err := s.Store.AccountCleanup(id)
	if err != nil {
		return err
	}
	release, ok := s.work.Try("created:"+id, "alias:"+strings.ToLower(item.Alias), "email:"+strings.ToLower(item.Email))
	if !ok {
		return model.ErrConflict
	}
	defer release()
	state, err := s.Store.AccountCreationView(ctx, item.Alias, item.Email)
	if err != nil {
		return err
	}
	if err = authorization.CheckAdmin(ctx, &state); err != nil {
		return err
	}
	if !item.CanManage(time.Now()) {
		return errors.New("account_creation_still_running")
	}
	if !confirmedAbsent {
		return s.recoverCreation(ctx, item)
	}
	if !item.Pending {
		return errors.New("account_creation_still_running")
	}
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	found, err := s.findCreated(check, item.Email, "")
	if err != nil {
		return model.ControlFailure(err)
	}
	if found != "" {
		return errors.New("account_creation_exists")
	}
	return s.Store.ResolveAbsentCreation(ctx, item)
}
