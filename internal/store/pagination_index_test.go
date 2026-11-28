package store

import (
	"strings"
	"testing"
)

func TestGrantPaginationSeeksHostIndex(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT data FROM remote_grants WHERE host_id=? AND id>? ORDER BY id LIMIT ?", "host", "cursor", 51)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var plan string
		if err := rows.Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(plan, "SEARCH remote_grants USING INDEX remote_grants_host (host_id=? AND id>?)")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("grant pagination no longer seeks within one host")
	}
}

func TestHistoryPaginationSeeksTimeIndex(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT data FROM remote_sessions
      WHERE state='closed' AND julianday(json_extract(data,'$.created_at')) <= julianday(?1)
      AND (julianday(json_extract(data,'$.created_at')),id) < (julianday(?1),?2)
      ORDER BY julianday(json_extract(data,'$.created_at')) DESC,id DESC LIMIT ?3`, "2026-11-28T00:00:00Z", "cursor", 51)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var plan string
		if err := rows.Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(plan, "SEARCH remote_sessions USING INDEX remote_history_time")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("history pagination scans newer history before the cursor")
	}
}
