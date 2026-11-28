package store

import (
	"context"
	"database/sql"
	"netgo/internal/model"
)

// A user filter reads the authoritative edit token even when that grant is not
// on the visible page. Both list and selection share the same snapshot contract.
func (s *Store) AdminGrantPage(ctx context.Context, c model.Credential, host, user, after string) (model.State, string, error) {
	if host == "" || len(host) > 128 || len(user) > 128 || !validSearch(after, "") {
		return model.State{}, "", model.ErrNotFound
	}
	var next string
	st, err := s.viewWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := readContext(ctx, tx, 0)
		if err != nil {
			return st, err
		}
		if err = readRowsContext(ctx, tx, "hosts", st.Hosts, "WHERE id=?", host); err != nil {
			return st, err
		}
		if len(st.Hosts) == 0 {
			return st, model.ErrNotFound
		}
		if user != "" {
			err = readRowsContext(ctx, tx, "remote_grants", st.RemoteGrants, "WHERE id=?", model.Hash(user+":"+host))
		} else {
			err = readRowsContext(ctx, tx, "remote_grants", st.RemoteGrants, "WHERE host_id=? AND id>? ORDER BY id LIMIT ?", host, after, adminPageSize+1)
			next = trimPage(st.RemoteGrants, adminPageSize)
		}
		if err != nil {
			return st, err
		}
		err = remoteDependencies(ctx, tx, &st, c)
		return st, err
	})
	return st, next, err
}
