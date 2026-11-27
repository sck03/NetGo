package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"time"
)

type Revision struct{ Desired, Observed uint64 }

func (s *Store) User(id string) (model.User, error) { return entity[model.User](s, "users", id) }
func (s *Store) Device(id string) (model.Device, error) {
	return entity[model.Device](s, "devices", id)
}

func entity[T any](s *Store, table, id string) (T, error) {
	var out T
	var raw []byte
	err := s.reader.QueryRow("SELECT data FROM "+table+" WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, model.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}

func (s *Store) Revision() (Revision, error) {
	var r Revision
	err := s.reader.QueryRow("SELECT revision,observed_revision FROM metadata WHERE id=1").Scan(&r.Desired, &r.Observed)
	return r, err
}

func (s *Store) Observe(revision uint64, message string) error {
	if message != "" {
		_, err := s.db.Exec("UPDATE metadata SET last_error=? WHERE id=1", message)
		return err
	}
	_, err := s.db.Exec("UPDATE metadata SET observed_revision=?,last_error='' WHERE id=1", revision)
	return err
}

func (s *Store) Lease(id string) (model.Lease, error) {
	var result model.Lease
	var raw []byte
	err := s.reader.QueryRow("SELECT data FROM vpn_leases WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, model.ErrForbidden
	}
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	return result, err
}

func (s *Store) PendingDevices(now time.Time, limit int) ([]model.Device, error) {
	return pending[model.Device](s, "devices", now, limit)
}

func (s *Store) PendingUsers(now time.Time, limit int) ([]model.User, error) {
	return pending[model.User](s, "users", now, limit)
}

func pending[T any](s *Store, table string, now time.Time, limit int) ([]T, error) {
	if limit < 1 || limit > 100 {
		limit = 16
	}
	rows, err := s.reader.Query("SELECT data FROM "+table+" WHERE json_extract(data,'$.delete_pending')=1 AND julianday(json_extract(data,'$.cleanup_after'))<=julianday(?) ORDER BY julianday(json_extract(data,'$.cleanup_after')),id LIMIT ?", now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw []byte
		var d T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// The idle scheduler checks expiry without cloning every domain table. All
// mutations still revalidate ownership inside the original commit fence.
func (s *Store) SessionCleanupDue(now time.Time) (bool, error) {
	var due bool
	err := s.reader.QueryRow(`SELECT
	 EXISTS(SELECT 1 FROM operation_keys WHERE julianday(json_extract(data,'$.expires_at')) <= julianday(?1)) OR
	 EXISTS(SELECT 1 FROM credentials WHERE julianday(json_extract(data,'$.expires_at')) <= julianday(?1)) OR
	 EXISTS(SELECT 1 FROM vpn_leases l JOIN users u ON u.id=l.user_id WHERE
	 julianday(json_extract(l.data,'$.expires_at')) <= julianday(?1) OR json_extract(u.data,'$.disabled')=1 OR
	 (json_extract(u.data,'$.expires_at') NOT LIKE '0001-%' AND julianday(json_extract(u.data,'$.expires_at')) <= julianday(?1))) OR
	 EXISTS(SELECT 1 FROM devices d WHERE json_extract(d.data,'$.client_type')='admin' AND NOT EXISTS(SELECT 1 FROM credentials c WHERE c.device_id=d.id))`, now.UTC().Format(time.RFC3339Nano)).Scan(&due)
	return due, err
}
