package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"reflect"
	"time"
)

// RenewalGate keeps an unchanged lease on the bounded refresh path. Returning
// false requests the current full membership under the same transaction fence.
type RenewalGate struct {
	Refresh func(model.Lease) (bool, error)
	Apply   func([]model.Lease) error
}

// VPNRenew loads the requesting identity and resources. Neither external gate
// path may escape the write fence; an apply or commit failure rolls back the row.
func (s *Store) VPNRenew(ctx context.Context, c model.Credential, id string, now time.Time, renew func(*model.State) error, gate RenewalGate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE metadata SET id=id WHERE id=1"); err != nil {
		return err
	}
	var lr, ur, dr, cr []byte
	// Authentication takes precedence even when revocation removed the lease too.
	err = tx.QueryRowContext(ctx, "SELECT data FROM credentials WHERE id=?", c.TokenHash).Scan(&cr)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT l.data,u.data,d.data FROM vpn_leases l
		JOIN users u ON u.id=json_extract(l.data,'$.user_id')
		JOIN devices d ON d.id=json_extract(l.data,'$.device_id')
		WHERE l.id=?`, id).Scan(&lr, &ur, &dr)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrForbidden
	}
	if err != nil {
		return err
	}
	var lease model.Lease
	var user model.User
	var device model.Device
	var credential model.Credential
	for _, item := range []struct {
		raw   []byte
		value any
	}{{lr, &lease}, {ur, &user}, {dr, &device}, {cr, &credential}} {
		if err = json.Unmarshal(item.raw, item.value); err != nil {
			return err
		}
	}
	st := model.NewState()
	st.Leases[id], st.Users[user.ID], st.Devices[device.ID], st.Credentials[c.TokenHash] = lease, user, device, credential
	resources, err := json.Marshal(user.Resources)
	if err != nil {
		return err
	}
	if err = readRowsContext(ctx, tx, "resources", st.Resources, "WHERE id IN (SELECT value FROM json_each(?))", string(resources)); err != nil {
		return err
	}
	if err = renew(&st); err != nil {
		return err
	}
	next, ok := st.Leases[id]
	identity := next
	identity.ExpiresAt, identity.Resources = lease.ExpiresAt, lease.Resources
	if !ok || len(st.Leases) != 1 || !reflect.DeepEqual(identity, lease) {
		return model.ErrForbidden
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE vpn_leases SET data=? WHERE id=?", string(raw), id); err != nil {
		return err
	}
	if gate.Refresh != nil {
		handled, err := gate.Refresh(next)
		if err != nil {
			return err
		}
		if handled {
			return tx.Commit()
		}
	}
	leases, err := gateMembership(ctx, tx, now)
	if err != nil {
		return err
	}
	if err = gate.Apply(leases); err != nil {
		return err
	}
	return tx.Commit()
}

// Expired memberships are excluded immediately; periodic sweep owns deletion
// and route reconciliation. Do not decode/clone every device, user or session.
func gateMembership(ctx context.Context, tx *sql.Tx, now time.Time) ([]model.Lease, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.data,json_extract(u.data,'$.expires_at') FROM vpn_leases l
		JOIN users u ON u.id=json_extract(l.data,'$.user_id')
		JOIN devices d ON d.id=json_extract(l.data,'$.device_id')
		WHERE json_extract(u.data,'$.disabled')=0 AND json_extract(u.data,'$.delete_pending')=0
		AND json_extract(d.data,'$.delete_pending')=0 AND json_extract(d.data,'$.user_id')=u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	leases := []model.Lease{}
	for rows.Next() {
		var raw []byte
		var expires string
		var lease model.Lease
		if err = rows.Scan(&raw, &expires); err != nil {
			return nil, err
		}
		until, err := time.Parse(time.RFC3339Nano, expires)
		if err != nil {
			return nil, err
		}
		if !until.IsZero() && !now.Before(until) {
			continue
		}
		if err = json.Unmarshal(raw, &lease); err != nil {
			return nil, err
		}
		if now.Before(lease.ExpiresAt) {
			leases = append(leases, lease)
		}
	}
	return leases, rows.Err()
}
