package model

import (
	"context"
	"errors"
)

// ControlFailure distinguishes an unavailable authority from an explicit denial.
// Callers must not extend credentials or leases when authority cannot be checked.
func ControlFailure(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return err
	}
	return errors.Join(ErrControlUnavailable, err)
}
