package store

import (
	"database/sql"
	"netgo/internal/model"
)

// VPN transitions need active remote routes for revision invalidation, but never
// edit retained history or remote idempotency keys. Entity deletion must continue
// to use Update, which loads the complete ownership graph.
func (s *Store) VPNUpdate(fn func(*model.State) error) error {
	return s.activeUpdate(allTables&^(remoteSessions|remoteOperations), fn)
}

// Host/grant changes close live sessions. Closed history is immutable on these
// paths and has no dependency on a grant row. Issuance uses RemoteAcquireUpdate
// with SQL generation/capacity checks over retained history.
func (s *Store) RemotePolicyUpdate(fn func(*model.State) error) error {
	return s.activeUpdate(remoteTables&^(remoteSessions|remoteOperations), fn)
}

func (s *Store) activeUpdate(tables scope, fn func(*model.State) error) error {
	return s.updateWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, tables)
		if err == nil {
			err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')")
		}
		return st, err
	}, fn)
}
