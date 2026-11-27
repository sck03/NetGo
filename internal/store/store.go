// Package store owns schema 5. There is intentionally no earlier-schema migration or
// dual-write path: a deployment must initialize a new database directory.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"netgo/internal/model"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const applicationID = 0x4e474f32

var ErrSchema = errors.New("database_schema_incompatible: initialize a fresh schema-5 database")

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, e
		}
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if e = s.initialize(); e != nil {
		db.Close()
		return nil, e
	}
	if path != ":memory:" {
		if e = os.Chmod(path, 0600); e != nil {
			db.Close()
			return nil, e
		}
	}
	return s, nil
}

func (s *Store) initialize() error {
	// Inspect BEFORE changing journal mode or creating tables in an old database.
	var version, owner, count int
	if e := s.db.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return e
	}
	if e := s.db.QueryRow("PRAGMA application_id").Scan(&owner); e != nil {
		return e
	}
	if e := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&count); e != nil {
		return e
	}
	if (count != 0 && (version != model.Schema || owner != applicationID)) || (count == 0 && (version != 0 || owner != 0)) {
		return ErrSchema
	}
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, e := s.db.Exec(q); e != nil {
			return e
		}
	}
	if count == 0 {
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, q := range schema {
			if _, e = tx.Exec(q); e != nil {
				return e
			}
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	_, e := s.View()
	return e
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) View() (model.State, error) { return s.view(allTables) }

func (s *Store) view(tables scope) (model.State, error) {
	return s.viewWith(func(tx *sql.Tx) (model.State, error) { return read(tx, tables) })
}

func (s *Store) viewWith(load func(*sql.Tx) (model.State, error)) (model.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return model.State{}, e
	}
	defer tx.Rollback()
	st, e := load(tx)
	if e == nil {
		e = tx.Commit()
	}
	return st, e
}

func readMap[T any](tx *sql.Tx, table string, target map[string]T) error {
	return readRows(tx, table, target, "")
}

func readRows[T any](tx *sql.Tx, table string, target map[string]T, where string, args ...any) error {
	rows, e := tx.Query("SELECT id,data FROM "+table+" "+where, args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		var item T
		if e = rows.Scan(&id, &raw); e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &item); e != nil {
			return e
		}
		target[id] = item
	}
	return rows.Err()
}

func read(tx *sql.Tx, tables scope) (model.State, error) {
	st := model.NewState()
	e := tx.QueryRow("SELECT revision,observed_revision,last_error FROM metadata WHERE id=1").Scan(&st.Revision, &st.ObservedRevision, &st.LastError)
	if e != nil {
		return st, e
	}
	for _, load := range []struct {
		table scope
		read  func() error
	}{
		{users, func() error { return readMap(tx, "users", st.Users) }},
		{resources, func() error { return readMap(tx, "resources", st.Resources) }},
		{devices, func() error { return readMap(tx, "devices", st.Devices) }},
		{leases, func() error { return readMap(tx, "vpn_leases", st.Leases) }},
		{credentials, func() error { return readMap(tx, "credentials", st.Credentials) }},
		{operations, func() error { return readMap(tx, "operation_keys", st.Operations) }},
		{hosts, func() error { return readMap(tx, "hosts", st.Hosts) }},
		{grants, func() error { return readMap(tx, "remote_grants", st.RemoteGrants) }},
		{remoteSessions, func() error { return readMap(tx, "remote_sessions", st.RemoteSessions) }},
		{remoteOperations, func() error { return readMap(tx, "remote_operations", st.RemoteOperations) }},
	} {
		if tables&load.table != 0 {
			if e = load.read(); e != nil {
				return st, e
			}
		}
	}
	return st, nil
}

// Save only changed rows; heartbeat/lease writes do not rewrite unrelated users,
// devices or the bounded audit log. Table names are compile-time constants.
func saveMap[T any](tx *sql.Tx, table string, before, after map[string]T) error {
	for id := range before {
		if _, exists := after[id]; !exists {
			if _, e := tx.Exec("DELETE FROM "+table+" WHERE id=?", id); e != nil {
				return e
			}
		}
	}
	for id, item := range after {
		if old, exists := before[id]; exists && reflect.DeepEqual(old, item) {
			continue
		}
		raw, e := json.Marshal(item)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO "+table+"(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", id, string(raw)); e != nil {
			return fmt.Errorf("%s: %w", table, e)
		}
	}
	return nil
}

func (s *Store) Update(fn func(*model.State) error) error { return s.update(allTables, fn) }

func (s *Store) update(tables scope, fn func(*model.State) error) error {
	return s.updateWith(func(tx *sql.Tx) (model.State, error) { return read(tx, tables) }, fn)
}

func (s *Store) updateWith(load func(*sql.Tx) (model.State, error), fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Acquire the write lock before reading. This fences separate processes too.
	if _, e = tx.Exec("UPDATE metadata SET id=id WHERE id=1"); e != nil {
		return e
	}
	before, e := load(tx)
	if e != nil {
		return e
	}
	after := before.Clone()
	if e = fn(&after); e != nil {
		return e
	}
	if after.Schema != model.Schema {
		return ErrSchema
	}
	model.PruneDependencies(&before, &after)
	if reflect.DeepEqual(before, after) {
		return nil
	}
	if !reflect.DeepEqual(before.Users, after.Users) || !reflect.DeepEqual(before.Resources, after.Resources) || !reflect.DeepEqual(model.RemoteRoutes(before), model.RemoteRoutes(after)) {
		after.Revision++
	}
	for _, save := range []func() error{
		func() error { return saveMap(tx, "users", before.Users, after.Users) },
		func() error { return saveMap(tx, "resources", before.Resources, after.Resources) },
		func() error { return saveMap(tx, "devices", before.Devices, after.Devices) },
		func() error { return saveMap(tx, "vpn_leases", before.Leases, after.Leases) },
		func() error { return saveMap(tx, "credentials", before.Credentials, after.Credentials) },
		func() error { return saveMap(tx, "operation_keys", before.Operations, after.Operations) },
		func() error { return saveMap(tx, "hosts", before.Hosts, after.Hosts) },
		func() error { return saveMap(tx, "remote_grants", before.RemoteGrants, after.RemoteGrants) },
		func() error { return saveRemoteSessions(tx, before.RemoteSessions, after.RemoteSessions) },
		func() error { return saveMap(tx, "remote_operations", before.RemoteOperations, after.RemoteOperations) },
		func() error { return appendAudit(tx, after.Audit) },
	} {
		if e = save(); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE metadata SET revision=?,observed_revision=?,last_error=? WHERE id=1", after.Revision, after.ObservedRevision, after.LastError); e != nil {
		return e
	}
	return tx.Commit()
}

// ConsumeNonce atomically consumes a digest across callers and restarts. Expiry
// includes the accepted request clock-skew window; full tables fail closed.
func (s *Store) ConsumeNonce(key string, now, expires time.Time) error {
	if len(key) != 64 || !expires.After(now) || expires.Sub(now) > 2*time.Minute {
		return model.ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM ticket_nonces WHERE expires_ms<=?", now.UnixMilli()); e != nil {
		return e
	}
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM ticket_nonces").Scan(&count); e != nil {
		return e
	}
	if count >= 65536 {
		return model.ErrUnauthorized
	}
	result, e := tx.Exec("INSERT OR IGNORE INTO ticket_nonces(id,expires_ms) VALUES(?,?)", key, expires.UnixMilli())
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrUnauthorized
	}
	return tx.Commit()
}
