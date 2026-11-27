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

func TestRenewDeltaOnlyChangesTargetAndRepairsExpiredMembership(t *testing.T) {
	now := time.Now()
	var script string
	cleared := 0
	n := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16", Now: func() time.Time { return now },
		Run: func(_ context.Context, text string) error { script = text; return nil }, Conntrack: func(context.Context, string) error { cleared++; return nil }}
	a := model.Lease{ID: "a", IP: "100.96.0.2", ExpiresAt: now.Add(30 * time.Second)}
	b := model.Lease{ID: "b", IP: "100.96.0.3", ExpiresAt: now.Add(time.Minute)}
	if err := n.Apply(context.Background(), []model.Lease{a, b}); err != nil {
		t.Fatal(err)
	}
	a.ExpiresAt = now.Add(90 * time.Second)
	ok, err := n.Renew(context.Background(), a)
	if err != nil || !ok || strings.Contains(script, b.IP) || strings.Contains(script, "flush") || cleared != 2 {
		t.Fatal(ok, err, script, cleared)
	}
	if !strings.Contains(script, "delete element inet netgo active4") || !strings.Contains(script, "timeout 90s") {
		t.Fatal(script)
	}
	changed := a
	changed.Resources = []model.Resource{{ID: "changed"}}
	if ok, err = n.Renew(context.Background(), changed); err != nil || ok {
		t.Fatal("policy change took delta path", ok, err)
	}
	now = now.Add(91 * time.Second)
	a.ExpiresAt = now.Add(time.Minute)
	if ok, err = n.Renew(context.Background(), a); err != nil || ok {
		t.Fatal("expired membership resurrected without fence", ok, err)
	}
}
