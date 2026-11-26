package store

import (
	"database/sql"
	"netgo/internal/model"
	"strings"
)

// Bounded parameter batches also keep this helper usable for large active sets.
// Table and column names are internal constants, never request parameters.
func readIDs[T any](tx *sql.Tx, table string, dst map[string]T, column string, ids []string) error {
	seen := make(map[string]bool, len(ids))
	args := make([]any, 0, 200)
	flush := func() error {
		if len(args) == 0 {
			return nil
		}
		err := readRows(tx, table, dst, "WHERE "+column+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+")", args...)
		args = args[:0]
		return err
	}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		args = append(args, id)
		if len(args) == cap(args) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// Load only the graph required to evaluate the selected hosts and sessions.
// It includes live credentials and both VPN leases; policy checks stay unchanged.
func remoteDependencies(tx *sql.Tx, st *model.State, c model.Credential) error {
	hostIDs, grantIDs, deviceIDs := []string{}, []string{}, []string{c.DeviceID}
	userIDs, credentialIDs := []string{c.UserID}, []string{c.TokenHash}
	for _, r := range st.RemoteSessions {
		if r.State == "closed" {
			continue
		}
		hostIDs = append(hostIDs, r.HostID)
		grantIDs = append(grantIDs, r.GrantID)
		deviceIDs = append(deviceIDs, r.ControllerID)
		userIDs = append(userIDs, r.UserID)
		credentialIDs = append(credentialIDs, r.ControllerAuth)
	}
	if err := readIDs(tx, "hosts", st.Hosts, "id", hostIDs); err != nil {
		return err
	}
	if err := readIDs(tx, "remote_grants", st.RemoteGrants, "id", grantIDs); err != nil {
		return err
	}
	for id := range st.Hosts {
		deviceIDs = append(deviceIDs, id)
	}
	if err := readIDs(tx, "devices", st.Devices, "id", deviceIDs); err != nil {
		return err
	}
	for _, d := range st.Devices {
		userIDs = append(userIDs, d.UserID)
	}
	for _, g := range st.RemoteGrants {
		userIDs = append(userIDs, g.UserID)
	}
	if err := readIDs(tx, "users", st.Users, "id", userIDs); err != nil {
		return err
	}
	if err := readIDs(tx, "credentials", st.Credentials, "id", credentialIDs); err != nil {
		return err
	}
	return readIDs(tx, "vpn_leases", st.Leases, "device_id", deviceIDs)
}
