package store

import (
	"database/sql"
	"encoding/json"
	"netgo/internal/model"
)

// Native inventory loads visible hosts and the complete dependencies needed to
// evaluate their busy/authorization state. Unrelated accounts never get decoded.
func inventoryState(tx *sql.Tx, c model.Credential) (model.State, error) {
	st, err := read(tx, 0)
	if err != nil {
		return st, err
	}
	if err = readRows(tx, "users", st.Users, "WHERE id=?", c.UserID); err != nil {
		return st, err
	}
	if st.Users[c.UserID].Role == "admin" {
		st, err = read(tx, hosts|grants)
		if err == nil {
			err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')")
		}
		if err == nil {
			err = remoteDependencies(tx, &st, c)
		}
		return st, err
	}
	if err = readRows(tx, "remote_grants", st.RemoteGrants, "WHERE user_id=?", c.UserID); err != nil {
		return st, err
	}
	hostIDs := map[string]bool{c.DeviceID: true}
	for _, g := range st.RemoteGrants {
		hostIDs[g.HostID] = true
	}
	if err = readRows(tx, "hosts", st.Hosts, "WHERE id IN (SELECT value FROM json_each(?))", keyJSON(hostIDs)); err != nil {
		return st, err
	}
	if err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND (controller_id=? OR host_id IN (SELECT value FROM json_each(?)))", c.DeviceID, keyJSON(hostIDs)); err != nil {
		return st, err
	}
	deviceIDs := hostIDs
	credentialIDs := map[string]bool{c.TokenHash: true}
	grantIDs := map[string]bool{}
	for _, r := range st.RemoteSessions {
		deviceIDs[r.ControllerID] = true
		deviceIDs[r.HostID] = true
		credentialIDs[r.ControllerAuth] = true
		grantIDs[r.GrantID] = true
	}
	userIDs := map[string]bool{c.UserID: true}
	for _, load := range []func() error{
		func() error {
			return readRows(tx, "devices", st.Devices, "WHERE id IN (SELECT value FROM json_each(?))", keyJSON(deviceIDs))
		},
		func() error {
			for _, d := range st.Devices {
				userIDs[d.UserID] = true
			}
			return readRows(tx, "users", st.Users, "WHERE id IN (SELECT value FROM json_each(?))", keyJSON(userIDs))
		},
		func() error {
			return readRows(tx, "credentials", st.Credentials, "WHERE id IN (SELECT value FROM json_each(?))", keyJSON(credentialIDs))
		},
		func() error {
			return readRows(tx, "remote_grants", st.RemoteGrants, "WHERE id IN (SELECT value FROM json_each(?))", keyJSON(grantIDs))
		},
		func() error {
			return readRows(tx, "vpn_leases", st.Leases, "WHERE device_id IN (SELECT value FROM json_each(?))", keyJSON(deviceIDs))
		},
	} {
		if err = load(); err != nil {
			return st, err
		}
	}
	return st, nil
}

func keyJSON(keys map[string]bool) string {
	values := make([]string, 0, len(keys))
	for key := range keys {
		values = append(values, key)
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}
