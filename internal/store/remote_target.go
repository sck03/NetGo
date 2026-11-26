package store

import (
	"database/sql"
	"netgo/internal/model"
	"time"
)

// Targeted transactions load the complete authorization dependency graph of one
// session. The ordinary commit fence, route revision and audit semantics remain
// shared with bulk mutations; retained history is never decoded on this path.
func remoteTarget(c model.Credential, id, host string) func(*sql.Tx) (model.State, error) {
	return func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err != nil {
			return st, err
		}
		if id != "" {
			if err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE id=?", id); err != nil {
				return st, err
			}
		}
		r := st.RemoteSessions[id]
		if r.HostID != "" {
			host = r.HostID
		}
		for _, load := range []func() error{
			func() error {
				return readRows(tx, "devices", st.Devices, "WHERE id IN (?,?,?)", c.DeviceID, r.ControllerID, host)
			},
			func() error {
				return readRows(tx, "users", st.Users, "WHERE id IN (?,?,?,?)", c.UserID, r.UserID, st.Devices[r.ControllerID].UserID, st.Devices[host].UserID)
			},
			func() error {
				return readRows(tx, "credentials", st.Credentials, "WHERE id IN (?,?)", c.TokenHash, r.ControllerAuth)
			},
			func() error { return readRows(tx, "hosts", st.Hosts, "WHERE id=?", host) },
			func() error { return readRows(tx, "remote_grants", st.RemoteGrants, "WHERE id=?", r.GrantID) },
			func() error {
				return readRows(tx, "vpn_leases", st.Leases, "WHERE device_id IN (?,?,?)", c.DeviceID, r.ControllerID, host)
			},
		} {
			if err = load(); err != nil {
				return st, err
			}
		}
		return st, nil
	}
}

func (s *Store) RemoteTargetView(c model.Credential, id, host string) (model.State, error) {
	return s.viewWith(remoteTarget(c, id, host))
}
func (s *Store) RemoteTargetUpdate(c model.Credential, id, host string, fn func(*model.State) error) error {
	return s.updateWith(remoteTarget(c, id, host), fn)
}

func (s *Store) RemoteSweep(now time.Time, fn func(*model.State) error) error {
	return s.updateWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, remoteTables & ^(remoteSessions|remoteOperations))
		if err == nil {
			err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') OR julianday(json_extract(data,'$.closed_at'))<julianday(?)", now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = readRows(tx, "remote_operations", st.RemoteOperations, "WHERE julianday(json_extract(data,'$.expires_at'))<=julianday(?)", now.UTC().Format(time.RFC3339Nano))
		}
		return st, err
	}, fn)
}
