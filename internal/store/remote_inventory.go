package store

import (
	"database/sql"
	"encoding/json"
	"netgo/internal/model"
)

// Native inventory exposes each device's own grants and sessions. Admin-wide
// authorization management belongs to the separate paged admin API.
func inventoryState(tx *sql.Tx, c model.Credential, after string) (NativeInventory, error) {
	out := NativeInventory{Hosts: map[string]bool{}}
	st, err := read(tx, 0)
	out.State = st
	if err != nil {
		return out, err
	}
	if err = readRows(tx, "users", st.Users, "WHERE id=?", c.UserID); err != nil {
		return out, err
	}
	admin := st.Users[c.UserID].Role == "admin"
	err = readRows(tx, "hosts", st.Hosts, `WHERE id>? AND (? OR id=? OR id IN (SELECT host_id FROM remote_grants WHERE user_id=?)) ORDER BY id LIMIT ?`, after, admin, c.DeviceID, c.UserID, adminPageSize+1)
	if err != nil {
		return out, err
	}
	out.NextAfter = trimPage(st.Hosts, adminPageSize)
	for id := range st.Hosts {
		out.Hosts[id] = true
	}
	if err = readRows(tx, "remote_grants", st.RemoteGrants, "WHERE user_id=? AND host_id IN (SELECT value FROM json_each(?))", c.UserID, keyJSON(out.Hosts)); err != nil {
		return out, err
	}
	// Include our own sessions on every page; other sessions only inform busy state.
	if err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND (controller_id=? OR host_id=? OR host_id IN (SELECT value FROM json_each(?)))", c.DeviceID, c.DeviceID, keyJSON(out.Hosts)); err != nil {
		return out, err
	}
	err = remoteDependencies(tx, &out.State, c)
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
