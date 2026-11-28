package store

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestNonceCapacityAndCounter(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	_, err = db.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<65536)
 INSERT INTO ticket_nonces(id,expires_ms) SELECT printf('%064d',x),? FROM n`, now.Add(time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ConsumeNonce(context.Background(), model.Hash("full"), now, now.Add(time.Minute)); !errors.Is(err, model.ErrControlCapacity) {
		t.Fatal("capacity became logout", err)
	}
	later := now.Add(2 * time.Minute)
	if err = db.ConsumeNonce(context.Background(), model.Hash("new"), later, later.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = db.ConsumeNonce(context.Background(), model.Hash("new"), later, later.Add(time.Minute)); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatal("replay accepted", err)
	}
	var count, actual int
	if err = db.db.QueryRow("SELECT nonce_count,(SELECT COUNT(*) FROM ticket_nonces) FROM metadata").Scan(&count, &actual); err != nil || count != 1 || actual != 1 {
		t.Fatal(count, actual, err)
	}
}
