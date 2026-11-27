package store

import (
	"context"
	"database/sql"
	"errors"
	"netgo/internal/model"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRejectOldDatabaseWithoutModification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TABLE netgo_state(id INTEGER PRIMARY KEY,data BLOB); INSERT INTO netgo_state VALUES(1,'{"schema":1}')`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if s, e := Open(path); !errors.Is(e, ErrSchema) {
		if s != nil {
			s.Close()
		}
		t.Fatal("accepted legacy database", e)
	}
	db, _ = sql.Open("sqlite", path)
	defer db.Close()
	var count int
	if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count); e != nil || count != 1 {
		t.Fatal("changed old database", count, e)
	}
}

func TestRejectPreviousSchemaWithoutMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema5.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE metadata(id INTEGER); PRAGMA application_id=1313296178; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); !errors.Is(err, ErrSchema) {
		if s != nil {
			s.Close()
		}
		t.Fatal("accepted schema 5", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatal("old database changed", version, err)
	}
}
func TestNonceConsumptionAcrossConnectionsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	a, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	key := model.Hash("nonce")
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			e := s.ConsumeNonce(context.Background(), key, now, now.Add(time.Minute))
			if e == nil {
				won.Add(1)
			} else if !errors.Is(e, model.ErrUnauthorized) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	a.Close()
	b.Close()
	if won.Load() != 1 {
		t.Fatal(won.Load())
	}
	a, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = a.ConsumeNonce(context.Background(), key, now, now.Add(time.Minute)); !errors.Is(e, model.ErrUnauthorized) {
		t.Fatal("restart allowed replay", e)
	}
	if e = a.ConsumeNonce(context.Background(), key, now.Add(2*time.Minute), now.Add(3*time.Minute)); e != nil {
		t.Fatal("expired nonce not reclaimed", e)
	}
}
func TestForeignKeysRejectOrphansAndRollback(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	e = s.Update(func(st *model.State) error { st.Devices["d"] = model.Device{ID: "d", UserID: "missing"}; return nil })
	if e == nil {
		t.Fatal("orphan device bypassed foreign key")
	}
	st, readErr := s.View()
	if readErr != nil || len(st.Devices) != 0 {
		t.Fatal(e, readErr, st.Devices)
	}
}
