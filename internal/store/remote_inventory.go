package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"netgo/internal/model"
)

// Native discovery loads page hosts and their live authorization dependencies.
// Grant and session records stay internal to the separate admin APIs.
func inventoryState(ctx context.Context, tx *sql.Tx, c model.Credential, after string) (NativeInventory, error) {
	out := NativeInventory{Hosts: map[string]bool{}}
	st, err := readContext(ctx, tx, 0)
	out.State = st
	if err != nil {
		return out, err
	}
	if err = readRowsContext(ctx, tx, "users", st.Users, "WHERE id=?", c.UserID); err != nil {
		return out, err
	}
	admin := st.Users[c.UserID].Role == "admin"
	err = readRowsContext(ctx, tx, "hosts", st.Hosts, `WHERE id>? AND (? OR id=? OR id IN (SELECT host_id FROM remote_grants WHERE user_id=?)) ORDER BY id LIMIT ?`, after, admin, c.DeviceID, c.UserID, adminPageSize+1)
	if err != nil {
		return out, err
	}
	out.NextAfter = trimPage(st.Hosts, adminPageSize)
	for id := range st.Hosts {
		out.Hosts[id] = true
	}
	if err = readRowsContext(ctx, tx, "remote_grants", st.RemoteGrants, "WHERE user_id=? AND host_id IN (SELECT value FROM json_each(?))", c.UserID, keyJSON(out.Hosts)); err != nil {
		return out, err
	}
	// Only page sessions are needed for busy state and its authorization graph.
	if err = readRowsContext(ctx, tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND host_id IN (SELECT value FROM json_each(?))", keyJSON(out.Hosts)); err != nil {
		return out, err
	}
	err = remoteDependencies(ctx, tx, &out.State, c)
	return out, err
}

func keyJSON(keys map[string]bool) string {
	values := make([]string, 0, len(keys))
	for key := range keys {
		values = append(values, key)
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}
