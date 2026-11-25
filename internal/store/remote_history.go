package store

import (
	"encoding/json"
	"netgo/internal/model"
	"time"
)

type RemoteHistory struct {
	Sessions   []model.RemoteSession `json:"sessions"`
	NextBefore string                `json:"next_before"`
}

// Inventory loads active sessions and the generation watermark in one snapshot.
// Closed session bodies never enter the frequently polled native response path.
func (s *Store) RemoteInventory(device string) (model.State, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return model.State{}, 0, err
	}
	defer tx.Rollback()
	st, err := read(tx, remoteTables&^(remoteSessions|remoteOperations))
	if err != nil {
		return st, 0, err
	}
	var generation uint64
	if err = tx.QueryRow("SELECT coalesce(max(json_extract(data,'$.generation')),0) FROM remote_sessions WHERE controller_id=?", device).Scan(&generation); err != nil {
		return st, 0, err
	}
	rows, err := tx.Query("SELECT id,data FROM remote_sessions WHERE state IN ('issued','active')")
	if err != nil {
		return st, 0, err
	}
	for rows.Next() {
		var id string
		var raw []byte
		var session model.RemoteSession
		if err = rows.Scan(&id, &raw); err == nil {
			err = json.Unmarshal(raw, &session)
		}
		if err != nil {
			rows.Close()
			return st, 0, err
		}
		st.RemoteSessions[id] = session
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		err = tx.Commit()
	}
	return st, generation, err
}

func (s *Store) RemoteCleanupDue(now time.Time) (bool, error) {
	var due bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM remote_sessions WHERE state IN ('issued','active') OR
	 julianday(json_extract(data,'$.expires_at')) < julianday(?1)-1) OR
	 EXISTS(SELECT 1 FROM remote_operations WHERE julianday(json_extract(data,'$.expires_at')) <= julianday(?1))`, now.UTC().Format(time.RFC3339Nano)).Scan(&due)
	return due, err
}

// IDs are immutable; a keyset cursor never shifts pages when a session closes.
// History is administrative and bounded; viewers use the active inventory only.
func (s *Store) RemoteHistory(before string, limit int) (RemoteHistory, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	page := RemoteHistory{Sessions: []model.RemoteSession{}}
	rows, err := s.db.Query(`SELECT data FROM remote_sessions WHERE state='closed' AND (?='' OR id<?) ORDER BY id DESC LIMIT ?`, before, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var session model.RemoteSession
		if err = rows.Scan(&raw); err != nil {
			return page, err
		}
		if err = json.Unmarshal(raw, &session); err != nil {
			return page, err
		}
		if len(page.Sessions) == limit {
			page.NextBefore = page.Sessions[limit-1].ID
			break
		}
		session.ControllerAuth = ""
		page.Sessions = append(page.Sessions, session)
	}
	return page, rows.Err()
}
