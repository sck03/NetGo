package store

import (
	"context"
	"database/sql"
	"netgo/internal/authorization"
	"netgo/internal/model"
)

// Creation needs uniqueness candidates and the current administrator, never VPN
// leases, remote history or operation keys. Commit still rechecks authorization.
func accountCreation(ctx context.Context, id, alias, email string) func(*sql.Tx) (model.State, error) {
	c := authorization.Credential(ctx)
	return func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err == nil {
			err = readRows(tx, "users", st.Users, "WHERE id IN (?,?) OR alias=lower(?) OR lower(json_extract(data,'$.email'))=lower(?)", id, c.UserID, alias, email)
		}
		if err == nil {
			err = readRows(tx, "devices", st.Devices, "WHERE id=?", c.DeviceID)
		}
		if err == nil {
			err = readRows(tx, "credentials", st.Credentials, "WHERE id=?", c.TokenHash)
		}
		return st, err
	}
}

func (s *Store) AccountCreationView(ctx context.Context, alias, email string) (model.State, error) {
	return s.viewWithContext(ctx, accountCreation(ctx, "", alias, email))
}

func (s *Store) AccountCreationUpdate(ctx context.Context, u model.User, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, accountCreation(ctx, u.ID, u.Alias, u.Email), fn)
}

// Retry bookkeeping changes one tombstone only; revocation already committed.
func (s *Store) UserCleanupUpdate(ctx context.Context, id string, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err == nil {
			err = readRows(tx, "users", st.Users, "WHERE id=?", id)
		}
		return st, err
	}, fn)
}

func (s *Store) DeviceCleanupUpdate(ctx context.Context, id string, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err == nil {
			err = readRows(tx, "devices", st.Devices, "WHERE id=?", id)
		}
		return st, err
	}, fn)
}
