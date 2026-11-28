package store

import (
	"context"
	"database/sql"
	"netgo/internal/model"
)

// VPN and account/resource/device transitions load all live ownership dependencies.
// Closed history is independent of deleted identities; no transition needs to
// decode it or remote idempotency rows to revoke active access atomically.
func (s *Store) VPNUpdate(ctx context.Context, fn func(*model.State) error) error {
	return s.activeUpdate(ctx, allTables&^(remoteSessions|remoteOperations), fn)
}

func (s *Store) activeUpdate(ctx context.Context, tables scope, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := readContext(ctx, tx, tables)
		if err == nil {
			err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')")
		}
		return st, err
	}, fn)
}
