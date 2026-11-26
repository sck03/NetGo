package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"time"
)

type RemoteHistory struct {
	Sessions   []model.RemoteSession `json:"sessions"`
	NextBefore string                `json:"next_before"`
}

// Inventory loads active sessions and the generation watermark in one snapshot.
// Closed session bodies never enter the frequently polled native response path.
func (s *Store) RemoteInventory(c model.Credential) (model.State, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return model.State{}, 0, err
	}
	defer tx.Rollback()
	st, err := inventoryState(tx, c)
	if err != nil {
		return st, 0, err
	}
	var generation uint64
	if err = tx.QueryRow("SELECT coalesce(max(json_extract(data,'$.generation')),0) FROM remote_sessions WHERE controller_id=?", c.DeviceID).Scan(&generation); err != nil {
		return st, 0, err
	}
	err = tx.Commit()
	return st, generation, err
}

func (s *Store) RemoteCleanupDue(now time.Time) (bool, error) {
	var due bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM remote_sessions WHERE state IN ('issued','active') OR
	 julianday(json_extract(data,'$.closed_at')) < julianday(?1)-1) OR
	 EXISTS(SELECT 1 FROM remote_operations WHERE julianday(json_extract(data,'$.expires_at')) <= julianday(?1))`, now.UTC().Format(time.RFC3339Nano)).Scan(&due)
	return due, err
}

type historyCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

// The cursor includes the immutable creation time, so expiry or removal of the
// last row on a page cannot invalidate the following page. IDs break time ties.
func (s *Store) RemoteHistory(before string, limit int) (RemoteHistory, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	page := RemoteHistory{Sessions: []model.RemoteSession{}}
	var cursor historyCursor
	if len(before) > 512 {
		return page, errors.New("invalid_remote_history_cursor")
	}
	if before != "" {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(before)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.At.IsZero() || cursor.ID == "" {
			return page, errors.New("invalid_remote_history_cursor")
		}
	}
	rows, err := s.db.Query(`SELECT data FROM remote_sessions WHERE state='closed' AND (?1='' OR
	 (julianday(json_extract(data,'$.created_at')),id) < (julianday(?2),?3))
	 ORDER BY julianday(json_extract(data,'$.created_at')) DESC,id DESC LIMIT ?4`, before, cursor.At.UTC().Format(time.RFC3339Nano), cursor.ID, limit+1)
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
			last := page.Sessions[limit-1]
			raw, _ := json.Marshal(historyCursor{At: last.CreatedAt, ID: last.ID})
			page.NextBefore = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		session.ControllerAuth = ""
		page.Sessions = append(page.Sessions, session)
	}
	return page, rows.Err()
}
