package store

import (
	"database/sql"
	"encoding/json"
	"netgo/internal/model"
)

func readKeys[T any](tx *sql.Tx, table string, target map[string]T, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return readRows(tx, table, target, "WHERE id IN (SELECT value FROM json_each(?))", string(raw))
}

// VPNRenew retains the full gate lease set and active remote route dependencies
// inside the commit fence. Idle identities, unrelated resources and operation
// history never participate in a renewal's decode/clone/diff work.
func (s *Store) VPNRenew(c model.Credential, fn func(*model.State) error) error {
	return s.updateWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, leases)
		if err != nil {
			return st, err
		}
		if err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')"); err != nil {
			return st, err
		}
		deviceIDs, credentialIDs := []string{c.DeviceID}, []string{c.TokenHash}
		var hostIDs, grantIDs []string
		for _, lease := range st.Leases {
			deviceIDs = append(deviceIDs, lease.DeviceID)
		}
		for _, r := range st.RemoteSessions {
			deviceIDs = append(deviceIDs, r.HostID, r.ControllerID)
			hostIDs = append(hostIDs, r.HostID)
			grantIDs = append(grantIDs, r.GrantID)
			credentialIDs = append(credentialIDs, r.ControllerAuth)
		}
		if err = readKeys(tx, "devices", st.Devices, deviceIDs); err != nil {
			return st, err
		}
		userIDs := []string{c.UserID}
		for _, d := range st.Devices {
			userIDs = append(userIDs, d.UserID)
		}
		for _, load := range []func() error{
			func() error { return readKeys(tx, "users", st.Users, userIDs) },
			func() error { return readKeys(tx, "resources", st.Resources, st.Users[c.UserID].Resources) },
			func() error { return readKeys(tx, "credentials", st.Credentials, credentialIDs) },
			func() error { return readKeys(tx, "hosts", st.Hosts, hostIDs) },
			func() error { return readKeys(tx, "remote_grants", st.RemoteGrants, grantIDs) },
		} {
			if err = load(); err != nil {
				return st, err
			}
		}
		return st, nil
	}, fn)
}
