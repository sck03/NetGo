package store

import (
	"context"
	"errors"
	"netgo/internal/model"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWALReadersAndCancelledWriter(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Name: "before"}
		st.Devices["d"] = model.Device{ID: "d", UserID: "u"}
		st.Credentials["c"] = model.Credential{UserID: "u", DeviceID: "d"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		done <- s.Update(func(st *model.State) error {
			u := st.Users["u"]
			u.Name = "after"
			st.Users["u"] = u
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, u, _, err := s.Identity(ctx, "c")
	if err != nil || u.Name != "before" {
		t.Fatal("reader blocked or observed uncommitted update", u, err)
	}
	ctx, cancelWrite := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelWrite()
	called := false
	if err = s.VPNUpdate(ctx, func(*model.State) error { called = true; return nil }); !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatal("cancelled queued write ran", err)
	}
	unblock()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if u, err = s.User("u"); err != nil || u.Name != "after" || u.EditRevision != 1 {
		t.Fatal("commit missing", u, err)
	}
	// Every pooled reader must be query-only and enforce foreign keys.
	for i := 0; i < 4; i++ {
		conn, err := s.reader.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, pragma := range []string{"foreign_keys", "query_only"} {
			var enabled int
			if err = conn.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&enabled); err != nil || enabled != 1 {
				t.Fatal(pragma, enabled, err)
			}
		}
	}
}

func TestCleanupQueriesUseExpiryIndexes(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct{ table, column, suffix, index string }{
		{"credentials", "expires_at", "", "credentials_expiry"},
		{"operation_keys", "expires_at", "", "operation_expiry"},
		{"remote_operations", "expires_at", "", "remote_operation_expiry"},
		{"remote_sessions", "closed_at", " AND state='closed'", "remote_history_expiry"},
	} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT 1 FROM "+tc.table+" WHERE julianday(json_extract(data,'$."+tc.column+"'))<=julianday(?)"+tc.suffix, time.Now().Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var id, parent, unused int
			var plan string
			if err = rows.Scan(&id, &parent, &unused, &plan); err != nil {
				t.Fatal(err)
			}
			found = found || strings.Contains(plan, tc.index)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !found {
			t.Fatal("cleanup scans table", tc.table)
		}
	}
}
