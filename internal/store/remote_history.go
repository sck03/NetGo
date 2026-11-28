package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"time"
)

type RemoteHistory struct {
	Sessions   []model.RemoteSession `json:"sessions"`
	NextBefore string                `json:"next_before"`
	Users      map[string]string     `json:"users"`
	Hosts      map[string]string     `json:"hosts"`
}

type NativeInventory struct {
	State      model.State
	Hosts      map[string]bool
	NextAfter  string
	Generation uint64
}

// Hosts, live dependencies and generation watermark share one SQL snapshot.
func (s *Store) RemoteInventory(ctx context.Context, c model.Credential, after string) (NativeInventory, error) {
	if !validSearch(after, "") {
		return NativeInventory{}, errors.New("invalid_remote_page")
	}
	tx, err := s.reader.BeginTx(ctx, nil)
	if err != nil {
		return NativeInventory{}, err
	}
	defer tx.Rollback()
	out, err := inventoryState(ctx, tx, c, after)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, "SELECT coalesce(max(json_extract(data,'$.generation')),0) FROM remote_sessions WHERE controller_id=?", c.DeviceID).Scan(&out.Generation)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) RemoteCleanupDue(ctx context.Context, now time.Time) (bool, error) {
	var due bool
	err := s.reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_sessions WHERE state IN ('issued','active')) OR
	 EXISTS(SELECT 1 FROM remote_sessions WHERE state='closed' AND julianday(json_extract(data,'$.closed_at')) < julianday(?1)-1) OR
	 EXISTS(SELECT 1 FROM remote_operations WHERE julianday(json_extract(data,'$.expires_at')) <= julianday(?1))`, now.UTC().Format(time.RFC3339Nano)).Scan(&due)
	return due, err
}

type historyCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

// The cursor includes the immutable creation time, so expiry or removal of the
// last row on a page cannot invalidate the following page. IDs break time ties.
func (s *Store) RemoteHistory(ctx context.Context, before string, limit int) (RemoteHistory, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	page := RemoteHistory{Sessions: []model.RemoteSession{}, Users: map[string]string{}, Hosts: map[string]string{}}
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
	rows, err := s.reader.QueryContext(ctx, `SELECT r.data,coalesce(json_extract(u.data,'$.alias'),''),coalesce(json_extract(h.data,'$.name'),'')
     FROM remote_sessions r LEFT JOIN users u ON u.id=r.user_id LEFT JOIN hosts h ON h.id=r.host_id
     WHERE r.state='closed' AND (?1='' OR
     (julianday(json_extract(r.data,'$.created_at')),r.id) < (julianday(?2),?3))
     ORDER BY julianday(json_extract(r.data,'$.created_at')) DESC,r.id DESC LIMIT ?4`, before, cursor.At.UTC().Format(time.RFC3339Nano), cursor.ID, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var session model.RemoteSession
		var user, host string
		if err = rows.Scan(&raw, &user, &host); err != nil {
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
		page.Users[session.UserID] = user
		page.Hosts[session.HostID] = host
		page.Sessions = append(page.Sessions, session)
	}
	return page, rows.Err()
}
