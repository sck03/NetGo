package gateway

import (
	"context"
	"netgo/internal/model"
	"strings"
	"testing"
	"time"
)

func TestRenewRefreshesTimeoutsAndPolicyChangeRebuilds(t *testing.T) {
	var script string
	cleared := 0
	n := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16", Run: func(_ context.Context, s string) error { script = s; return nil }, Conntrack: func(context.Context, string) error { cleared++; return nil }}
	l := model.Lease{ID: "a", IP: "100.96.0.2", ExpiresAt: time.Now().Add(time.Minute)}
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil {
		t.Fatal(err)
	}
	l.ExpiresAt = time.Now().Add(90 * time.Second)
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "rule") || !strings.Contains(script, "flush set inet netgo active4") || cleared != 1 {
		t.Fatal("renew rebuilt policy or conntrack", script, cleared)
	}
	l.Resources = []model.Resource{{ID: "nas", Name: "NAS", CIDR: "192.168.1.2/32", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}}
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "flush table inet netgo") || cleared != 2 {
		t.Fatal("policy change bypassed full fence", script, cleared)
	}
	n.lastFull = time.Now().Add(-time.Minute)
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "flush table inet netgo") {
		t.Fatal("periodic repair omitted")
	}
}
