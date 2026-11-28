package store

import (
	"context"
	"encoding/json"
	"netgo/internal/authorization"
	"netgo/internal/model"
	"time"
)

type AccountCleanupTask struct {
	AccountCleanup
	CanManage bool `json:"can_manage"`
}

type AccountCleanupPage struct {
	Tasks     []AccountCleanupTask `json:"tasks"`
	NextAfter string               `json:"next_after"`
}

func (s *Store) AccountCleanups(ctx context.Context, after string) (AccountCleanupPage, error) {
	page := AccountCleanupPage{Tasks: []AccountCleanupTask{}}
	now := time.Now()
	if !validSearch(after, "") {
		return page, model.ErrNotFound
	}
	rows, err := s.reader.QueryContext(ctx, "SELECT data FROM account_cleanups WHERE id>? ORDER BY id LIMIT ?", after, adminPageSize+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item AccountCleanup
		if err = rows.Scan(&raw); err != nil {
			return page, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return page, err
		}
		if len(page.Tasks) == adminPageSize {
			page.NextAfter = page.Tasks[len(page.Tasks)-1].ID
			break
		}
		page.Tasks = append(page.Tasks, AccountCleanupTask{AccountCleanup: item, CanManage: item.CanManage(now)})
	}
	return page, rows.Err()
}

// An explicit operator resolution fences both the administrator and the exact
// job observed before IO. It never removes a locally published account.
func (s *Store) ResolveAbsentCreation(ctx context.Context, item AccountCleanup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE metadata SET id=id WHERE id=1"); err != nil {
		return err
	}
	st, err := accountCreation(ctx, "", item.Alias, item.Email)(tx)
	if err != nil {
		return err
	}
	if err = authorization.CheckAdmin(ctx, &st); err != nil {
		return err
	}
	expected, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM account_cleanups WHERE id=? AND data=?", item.ID, string(expected))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return model.ErrEditConflict
	}
	st.Record(authorization.Actor(ctx), "account.creation.resolve_absent", item.ID)
	if err = appendAudit(tx, st.Audit); err != nil {
		return err
	}
	return tx.Commit()
}
