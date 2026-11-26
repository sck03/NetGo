package store

import (
	"encoding/json"
	"netgo/internal/model"
	"time"
)

// AccountCleanup owns only an upstream account created by an incomplete local
// operation. It has no local alias, credential or permission to invalidate.
type AccountCleanup struct {
	ID string `json:"id"`
	model.CleanupRetry
}

// QueueAccountCleanup runs before publishing the local account. Never claim an
// existing local account or another creation's compensation record.
func (s *Store) QueueAccountCleanup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(AccountCleanup{ID: id})
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT OR IGNORE INTO account_cleanups(id,data)
	 SELECT ?1,?2 WHERE NOT EXISTS(SELECT 1 FROM users WHERE id=?1)`, id, string(b))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return model.ErrConflict
	}
	return err
}

func (s *Store) PendingAccountCleanups(now time.Time, limit int) ([]AccountCleanup, error) {
	if limit < 1 || limit > 100 {
		limit = 16
	}
	rows, err := s.db.Query(`SELECT data FROM account_cleanups
	 WHERE julianday(json_extract(data,'$.cleanup_after'))<=julianday(?)
	 ORDER BY julianday(json_extract(data,'$.cleanup_after')),id LIMIT ?`, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AccountCleanup{}
	for rows.Next() {
		var raw []byte
		var item AccountCleanup
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AccountCleanup(id string) (AccountCleanup, error) {
	return entity[AccountCleanup](s, "account_cleanups", id)
}

func (s *Store) FinishAccountCleanup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM account_cleanups WHERE id=?", id)
	return err
}

func (s *Store) DeferAccountCleanup(item AccountCleanup, now time.Time) error {
	item.Failed(now)
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec("UPDATE account_cleanups SET data=? WHERE id=?", string(b), item.ID)
	return err
}
