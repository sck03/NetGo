package store

import (
	"context"
	"database/sql"
	"netgo/internal/model"
	"time"
)

// Targeted transactions load the complete authorization dependency graph of one
// session. The ordinary commit fence, route revision and audit semantics remain
// shared with bulk mutations; retained history is never decoded on this path.
func remoteTarget(ctx context.Context, c model.Credential, id, host string) func(*sql.Tx) (model.State, error) {
	return func(tx *sql.Tx) (model.State, error) {
		st, err := readContext(ctx, tx, 0)
		if err != nil {
			return st, err
		}
		if id != "" {
			if err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE id=?", id); err != nil {
				return st, err
			}
		}
		r := st.RemoteSessions[id]
		if r.HostID != "" {
			host = r.HostID
		}
		for _, load := range []func() error{
			func() error {
				return readRowsContext(ctx, tx, "devices", st.Devices, "WHERE id IN (?,?,?)", c.DeviceID, r.ControllerID, host)
			},
			func() error {
				return readRowsContext(ctx, tx, "users", st.Users, "WHERE id IN (?,?,?,?)", c.UserID, r.UserID, st.Devices[r.ControllerID].UserID, st.Devices[host].UserID)
			},
			func() error {
				return readRowsContext(ctx, tx, "credentials", st.Credentials, "WHERE id IN (?,?)", c.TokenHash, r.ControllerAuth)
			},
			func() error { return readRowsContext(ctx, tx, "hosts", st.Hosts, "WHERE id=?", host) },
			func() error {
				return readRowsContext(ctx, tx, "remote_grants", st.RemoteGrants, "WHERE id=?", r.GrantID)
			},
			func() error {
				return readRowsContext(ctx, tx, "vpn_leases", st.Leases, "WHERE device_id IN (?,?,?)", c.DeviceID, r.ControllerID, host)
			},
		} {
			if err = load(); err != nil {
				return st, err
			}
		}
		return st, nil
	}
}

func (s *Store) RemoteTargetView(ctx context.Context, c model.Credential, id, host string) (model.State, error) {
	return s.viewWithContext(ctx, remoteTarget(ctx, c, id, host))
}
func (s *Store) RemoteTargetUpdate(ctx context.Context, c model.Credential, id, host string, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, remoteTarget(ctx, c, id, host), fn)
}

func (s *Store) RemoteSweep(ctx context.Context, now time.Time, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := readContext(ctx, tx, 0)
		if err == nil {
			err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')")
		}
		if err == nil {
			err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state='closed' AND julianday(json_extract(data,'$.closed_at'))<julianday(?) ORDER BY julianday(json_extract(data,'$.closed_at')),id LIMIT 256", now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = readRowsContext(ctx, tx, "remote_operations", st.RemoteOperations, "WHERE julianday(json_extract(data,'$.expires_at'))<=julianday(?) ORDER BY julianday(json_extract(data,'$.expires_at')) LIMIT 256", now.UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = remoteDependencies(ctx, tx, &st, model.Credential{})
		}
		return st, err
	}, fn)
}
