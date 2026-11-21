package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	_ "modernc.org/sqlite"
	"netgo/internal/model"
	"os"
	"path/filepath"
	"sync"
)

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
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000", "CREATE TABLE IF NOT EXISTS netgo_state (id INTEGER PRIMARY KEY CHECK(id=1), data BLOB NOT NULL)"} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	b, _ := json.Marshal(model.NewState())
	if _, e = db.Exec("INSERT OR IGNORE INTO netgo_state(id,data) VALUES(1,?)", b); e != nil {
		db.Close()
		return nil, e
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0600)
	}
	s := &Store{db: db}
	st, e := s.View()
	if e != nil || st.Schema != 1 {
		db.Close()
		return nil, errors.New("invalid/unsupported database schema")
	}
	return s, nil
}
func (s *Store) Close() error               { return s.db.Close() }
func (s *Store) View() (model.State, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.read() }
func (s *Store) read() (model.State, error) {
	var b []byte
	var st model.State
	e := s.db.QueryRow("SELECT data FROM netgo_state WHERE id=1").Scan(&b)
	if e != nil {
		return st, e
	}
	e = json.Unmarshal(b, &st)
	return st, e
}
func (s *Store) Update(fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Acquire SQLite's write lock before reading or changing the kernel gate.
	if _, e = tx.Exec("UPDATE netgo_state SET id=id WHERE id=1"); e != nil {
		return e
	}
	var raw []byte
	var st model.State
	if e = tx.QueryRow("SELECT data FROM netgo_state WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	if e = json.Unmarshal(raw, &st); e != nil {
		return e
	}
	if e = fn(&st); e != nil {
		return e
	}
	st.Revision++
	b, e := json.Marshal(st)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE netgo_state SET data=? WHERE id=1", b); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Snapshot() ([]byte, error) {
	st, e := s.View()
	if e != nil {
		return nil, e
	}
	return json.MarshalIndent(st, "", "  ")
}
