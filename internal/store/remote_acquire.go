package store

import (
	"database/sql"
	"netgo/internal/model"
	"time"
)

type RemoteAcquireLimits struct {
	Generation uint64
	Retained   int
}

// RemoteAcquireUpdate loads only the sessions that can compete with this user or
// host, plus their complete authorization graph. Aggregate replay/capacity checks
// share the write transaction; retained history is never decoded or cloned.
func (s *Store) RemoteAcquireUpdate(c model.Credential, host, operation string, now time.Time, fn func(*model.State, RemoteAcquireLimits) error) error {
	var limits RemoteAcquireLimits
	return s.updateWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err != nil {
			return st, err
		}
		for _, load := range []func() error{
			func() error { return readRows(tx, "hosts", st.Hosts, "WHERE id=?", host) },
			func() error {
				return readRows(tx, "remote_grants", st.RemoteGrants, "WHERE user_id=? AND host_id=?", c.UserID, host)
			},
			func() error {
				return readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND (user_id=? OR host_id=?)", c.UserID, host)
			},
			func() error { return readRows(tx, "remote_operations", st.RemoteOperations, "WHERE id=?", operation) },
			func() error { return remoteDependencies(tx, &st, c) },
			func() error {
				return tx.QueryRow("SELECT coalesce(max(json_extract(data,'$.generation')),0) FROM remote_sessions WHERE controller_id=?", c.DeviceID).Scan(&limits.Generation)
			},
			func() error {
				return tx.QueryRow("SELECT count(*) FROM remote_sessions WHERE state!='closed' OR julianday(json_extract(data,'$.closed_at'))>=julianday(?)", now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano)).Scan(&limits.Retained)
			},
		} {
			if err = load(); err != nil {
				return st, err
			}
		}
		return st, nil
	}, func(st *model.State) error { return fn(st, limits) })
}
