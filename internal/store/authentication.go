package store

import (
	"database/sql"
	"netgo/internal/model"
)

// EnrollmentUpdate loads only the account and its devices for quota validation.
// Credential insertion and the device quota remain inside the shared write fence.
func (s *Store) EnrollmentUpdate(userID string, fn func(*model.State) error) error {
	return s.updateWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err == nil {
			err = readRows(tx, "users", st.Users, "WHERE id=?", userID)
		}
		if err == nil {
			err = readRows(tx, "devices", st.Devices, "WHERE user_id=?", userID)
		}
		return st, err
	}, fn)
}

// CredentialUpdate rotates only one device credential and the active sessions
// bound to it. The peer routes are unchanged by an atomic token replacement.
func (s *Store) CredentialUpdate(userID, deviceID, tokenHash string, fn func(*model.State) error) error {
	return s.updateWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err == nil {
			err = readRows(tx, "users", st.Users, "WHERE id=?", userID)
		}
		if err == nil {
			err = readRows(tx, "devices", st.Devices, "WHERE id=?", deviceID)
		}
		if err == nil {
			err = readRows(tx, "credentials", st.Credentials, "WHERE id=?", tokenHash)
		}
		if err == nil {
			err = readRows(tx, "remote_sessions", st.RemoteSessions,
				"WHERE controller_id=? AND state IN ('issued','active') AND json_extract(data,'$.controller_auth')=?", deviceID, tokenHash)
		}
		return st, err
	}, fn)
}
