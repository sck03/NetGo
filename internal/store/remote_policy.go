package store

import (
	"context"
	"database/sql"
	"netgo/internal/model"
)

// Only the edited host/grant and affected live sessions enter this transaction.
// Dependencies still include both leases, credentials, owners and peer identity.
// updateWithContext preserves the commit fence, route revision and audit writes.
func (s *Store) RemoteHostUpdate(ctx context.Context, c model.Credential, host string, fn func(*model.State) error) error {
	return s.remotePolicyUpdate(ctx, c, host, "", "", fn)
}
func (s *Store) RemoteGrantUpdate(ctx context.Context, c model.Credential, g model.RemoteGrant, fn func(*model.State) error) error {
	return s.remotePolicyUpdate(ctx, c, g.HostID, g.ID, g.UserID, fn)
}
func (s *Store) remotePolicyUpdate(ctx context.Context, c model.Credential, host, grant, user string, fn func(*model.State) error) error {
	return s.updateWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := readContext(ctx, tx, 0)
		if err != nil {
			return st, err
		}
		if grant != "" {
			if err = readRowsContext(ctx, tx, "remote_grants", st.RemoteGrants, "WHERE id=?", grant); err != nil {
				return st, err
			}
			if host == "" {
				host = st.RemoteGrants[grant].HostID
			}
			if host != "" {
				// The partial host index contains at most one active session.
				err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND host_id=? AND json_extract(data,'$.grant_id')=?", host, grant)
			} else {
				// Removing an already-missing grant still closes any orphaned ticket.
				err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND json_extract(data,'$.grant_id')=?", grant)
			}
		} else {
			err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND host_id=?", host)
		}
		if err != nil {
			return st, err
		}
		if err = readRowsContext(ctx, tx, "hosts", st.Hosts, "WHERE id=?", host); err != nil {
			return st, err
		}
		// A new grant has no stored row yet, so load its target user explicitly.
		if err = readRowsContext(ctx, tx, "users", st.Users, "WHERE id=?", user); err != nil {
			return st, err
		}
		err = remoteDependencies(ctx, tx, &st, c)
		return st, err
	}, fn)
}
