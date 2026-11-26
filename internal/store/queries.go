package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"netgo/internal/model"
)

// Identity reads only the indexed credential and its two owners in one snapshot.
func (s *Store) Identity(hash string) (model.Credential, model.User, model.Device, error) {
	var c model.Credential
	var u model.User
	var d model.Device
	var cr, ur, dr []byte
	err := s.db.QueryRow(`SELECT c.data,u.data,d.data FROM credentials c JOIN users u ON u.id=json_extract(c.data,'$.user_id') JOIN devices d ON d.id=json_extract(c.data,'$.device_id') WHERE c.id=?`, hash).Scan(&cr, &ur, &dr)
	if errors.Is(err, sql.ErrNoRows) {
		err = model.ErrUnauthorized
	}
	if err != nil {
		return c, u, d, err
	}
	for _, v := range []struct {
		raw []byte
		dst any
	}{{cr, &c}, {ur, &u}, {dr, &d}} {
		if err = json.Unmarshal(v.raw, v.dst); err != nil {
			return c, u, d, err
		}
	}
	c.TokenHash = hash
	return c, u, d, nil
}

type AuditEntry struct {
	Sequence int64 `json:"sequence"`
	model.Event
}
type AuditPage struct {
	Events     []AuditEntry `json:"events"`
	NextBefore int64        `json:"next_before"`
}

// Audit uses a stable sequence cursor. Concurrent appends never shift older pages.
func (s *Store) Audit(before int64, limit int) (AuditPage, error) {
	page := AuditPage{Events: []AuditEntry{}}
	if limit < 1 || limit > 100 || before < 0 {
		return page, errors.New("invalid audit cursor or limit")
	}
	rows, err := s.db.Query("SELECT sequence,data FROM audit_events WHERE (?=0 OR sequence<?) ORDER BY sequence DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item AuditEntry
		var raw []byte
		if err = rows.Scan(&item.Sequence, &raw); err != nil {
			return page, err
		}
		if len(page.Events) == limit {
			page.NextBefore = page.Events[len(page.Events)-1].Sequence
			break
		}
		if err = json.Unmarshal(raw, &item.Event); err != nil {
			return page, err
		}
		page.Events = append(page.Events, item)
	}
	return page, rows.Err()
}

// Updates receive only newly appended events; audit history is never loaded into
// policy transactions. Retention is enforced by SQLite after appending.
func appendAudit(tx *sql.Tx, events []model.Event) error {
	if len(events) == 0 {
		return nil
	}
	for _, event := range events {
		raw, e := json.Marshal(event)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO audit_events(data) VALUES(?)", string(raw)); e != nil {
			return e
		}
	}
	_, e := tx.Exec("DELETE FROM audit_events WHERE sequence <= (SELECT coalesce(max(sequence),0)-10000 FROM audit_events)")
	return e
}

func saveRemoteSessions(tx *sql.Tx, before, after map[string]model.RemoteSession) error {
	// SQLite partial unique indexes are immediate: release occupied hosts first.
	for id, old := range before {
		if next, ok := after[id]; ok && old.State != "closed" && next.State == "closed" {
			raw, e := json.Marshal(next)
			if e != nil {
				return e
			}
			if _, e = tx.Exec("UPDATE remote_sessions SET data=? WHERE id=?", string(raw), id); e != nil {
				return e
			}
		}
	}
	return saveMap(tx, "remote_sessions", before, after)
}

// Read gives the caller an isolated snapshot without acquiring SQLite's write lock.
func (s *Store) Read(fn func(*model.State) error) error {
	st, e := s.View()
	if e != nil {
		return e
	}
	return fn(&st)
}

// Scopes keep high-frequency queries independent from unrelated domain tables.
// Write scopes are package-private; domain entry points name the loaded contract.
type scope uint16

const (
	users scope = 1 << iota
	resources
	devices
	leases
	credentials
	operations
	hosts
	grants
	remoteSessions
	remoteOperations
)
const allTables = users | resources | devices | leases | credentials | operations | hosts | grants | remoteSessions | remoteOperations
const remoteTables = users | devices | leases | credentials | hosts | grants | remoteSessions | remoteOperations

func (s *Store) RemoteUpdate(fn func(*model.State) error) error { return s.update(remoteTables, fn) }
func (s *Store) AdminView() (model.State, error)                { return s.view(users | resources | devices | leases) }
func (s *Store) PolicyView() (model.State, error) {
	return s.viewWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, users|resources|devices|leases|credentials|hosts|grants)
		if err == nil {
			err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')")
		}
		return st, err
	})
}
func (s *Store) UserByAlias(alias string) (model.User, error) {
	var u model.User
	var raw []byte
	e := s.db.QueryRow("SELECT data FROM users WHERE alias=lower(?)", alias).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return u, model.ErrUnauthorized
	}
	if e != nil {
		return u, e
	}
	e = json.Unmarshal(raw, &u)
	return u, e
}
