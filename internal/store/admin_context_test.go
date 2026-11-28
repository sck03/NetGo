package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdminReadsRespectCancellation(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Hold the sole reader connection so a missing context would hang this test.
	conn, err := s.reader.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, call := range map[string]func() error{
		"summary": func() error { _, _, e := s.AdminSummary(ctx, time.Now()); return e },
		"page":    func() error { _, _, e := s.AdminPage(ctx, "users", "", 50); return e },
		"audit":   func() error { _, e := s.Audit(ctx, 0, 50); return e },
		"users":   func() error { _, e := s.UserChoices(ctx, "", ""); return e },
		"history": func() error { _, e := s.RemoteHistory(ctx, "", 50); return e },
	} {
		t.Run(name, func(t *testing.T) {
			if e := call(); !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		})
	}
}

func TestAccountCleanupEligibilityComesFromServer(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	for _, item := range []AccountCleanup{
		{ID: "old", Alias: "old", Email: "old@test", Pending: true, CreatedAt: now.Add(-11 * time.Minute)},
		{ID: "new", Alias: "new", Email: "new@test", Pending: true, CreatedAt: now},
		{ID: "cleanup", Alias: "cleanup", Email: "cleanup@test", Pending: false, CreatedAt: now},
	} {
		if err = s.ReserveAccountCreation(item); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.AccountCleanups(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 3 {
		t.Fatal(page)
	}
	for _, task := range page.Tasks {
		if task.CanManage != (task.ID != "new") {
			t.Fatal(task)
		}
	}
	if (AccountCleanup{Pending: true}).CanManage(now) {
		t.Fatal("missing creation time accepted")
	}
	if !(AccountCleanup{Pending: true, CreatedAt: now.Add(-10 * time.Minute)}).CanManage(now) {
		t.Fatal("ten-minute boundary rejected")
	}
}
