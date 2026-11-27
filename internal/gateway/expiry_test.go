package gateway

import (
	"context"
	"netgo/internal/model"
	"strings"
	"testing"
	"time"
)

func TestApplyDoesNotReopenLeaseExpiredDuringCleanup(t *testing.T) {
	now := time.Now()
	lease := model.Lease{ID: "a", IP: "100.96.0.2", ExpiresAt: now.Add(2 * time.Second), Resources: []model.Resource{{ID: "nas", Name: "NAS", CIDR: "192.168.1.20/32", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}}}
	var applied []string
	n := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16",
		Now:       func() time.Time { return now },
		Run:       func(_ context.Context, script string) error { applied = append(applied, script); return nil },
		Conntrack: func(context.Context, string) error { now = now.Add(3 * time.Second); return nil },
	}
	if err := n.Apply(context.Background(), []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected guard and final batch, got %d", len(applied))
	}
	for _, script := range applied {
		if strings.Contains(script, "100.96.0.2") || strings.Contains(script, "192.168.1.20") {
			t.Fatalf("expired scope reopened: %s", script)
		}
	}
	if len(n.active) != 0 || len(n.pending) != 0 {
		t.Fatal("expired membership retained in cache")
	}
	// Reusing an IP later must still clean conntrack before reopening it.
	lease.ExpiresAt = now.Add(time.Minute)
	if err := n.Apply(context.Background(), []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 4 || !strings.Contains(applied[3], "timeout 57s") {
		t.Fatalf("deadline not recomputed: %v", applied)
	}
}

func TestApplyCapturesTimeAfterInspection(t *testing.T) {
	now := time.Now()
	lease := model.Lease{ID: "a", IP: "100.96.0.2", ExpiresAt: now.Add(2 * time.Second)}
	n := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16",
		Now:     func() time.Time { return now },
		Inspect: func(context.Context) error { now = now.Add(3 * time.Second); return nil },
		Run: func(_ context.Context, script string) error {
			if strings.Contains(script, "100.96.0.2") {
				t.Fatal("inspection resurrected lease")
			}
			return nil
		},
	}
	if err := n.Apply(context.Background(), []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
}
