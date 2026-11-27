package store

import (
	"encoding/json"
	"netgo/internal/model"
	"time"
)

// AccountCleanup reserves a creation before upstream IO and owns its compensation
// until publication. It never stores credentials or an initial password.
type AccountCleanup struct {
	ID      string `json:"id"`
	Alias   string `json:"alias"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Pending bool   `json:"pending"`
	model.CleanupRetry
}

// ReserveAccountCreation precedes the upstream POST. Alias and email reservations
// survive restarts and fence other processes using this database as well.
func (s *Store) ReserveAccountCreation(item AccountCleanup) error {
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT OR IGNORE INTO account_cleanups(id,data)
	 SELECT ?1,?2 WHERE NOT EXISTS(SELECT 1 FROM users WHERE id=?1 OR (?3!='' AND lower(json_extract(data,'$.alias'))=lower(?3))
	 OR (?4!='' AND lower(json_extract(data,'$.email'))=lower(?4)))`, item.ID, string(b), item.Alias, item.Email)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return model.ErrConflict
	}
	return err
}

// ResolveAccountCreation retains the reservation until local publication or
// compensation completes. A failed write leaves the email intent recoverable.
func (s *Store) ResolveAccountCreation(item AccountCleanup, id string) error {
	previous := item.ID
	item.ID, item.Pending = id, false
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE OR IGNORE account_cleanups SET id=?,data=? WHERE id=?`, id, string(b), previous)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return model.ErrConflict
	}
	return err
}

func (s *Store) PendingAccountCleanups(now time.Time, limit int) ([]AccountCleanup, error) {
	if limit < 1 || limit > 100 {
		limit = 16
	}
	rows, err := s.reader.Query(`SELECT data FROM account_cleanups
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
	_, err := s.db.Exec("DELETE FROM account_cleanups WHERE id=?", id)
	return err
}

func (s *Store) DeferAccountCleanup(item AccountCleanup, now time.Time) error {
	item.Failed(now)
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE account_cleanups SET data=? WHERE id=?", string(b), item.ID)
	return err
}
