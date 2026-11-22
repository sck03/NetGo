package gateway

import (
	"context"
	"errors"
	"netgo/internal/model"
	"strings"
	"testing"
	"time"
)

func TestGatePrecedesConntrackAndHasTimeout(t *testing.T) {
	now := time.Now()
	l := model.Lease{ID: "a", IP: "100.96.0.2", ExpiresAt: now.Add(time.Minute), Resources: []model.Resource{{ID: "nas", Name: "NAS", CIDR: "192.168.1.20/32", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}}}
	script, e := Render("wt0", "eth0", "100.96.0.0/16", []model.Lease{l}, now)
	if e != nil {
		t.Fatal(e)
	}
	for _, must := range []string{"priority -150", "timeout 60s", "iifname \"wt0\" counter drop", "tcp dport { 443 }", "flush table inet netgo"} {
		if !strings.Contains(script, must) {
			t.Fatal("missing", must)
		}
	}
	if strings.Contains(script, "flush ruleset") {
		t.Fatal("host firewall flushed")
	}
	if strings.Index(script, "iifname \"wt0\" counter drop") > strings.Index(script, "ct state established") {
		t.Fatal("gate follows conntrack")
	}
}
func TestRevocationClosesGateBeforeScopedCleanupAndRetries(t *testing.T) {
	l := model.Lease{ID: "first", IP: "100.96.0.2", ExpiresAt: time.Now().Add(time.Minute)}
	var calls []string
	fail := false
	n := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16", Run: func(context.Context, string) error { calls = append(calls, "gate"); return nil }, Conntrack: func(_ context.Context, ip string) error {
		calls = append(calls, ip)
		if fail {
			return errors.New("cleanup unavailable")
		}
		return nil
	}}
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil {
		t.Fatal(err)
	}
	fail = true
	calls = nil
	if err := n.Apply(context.Background(), nil); err == nil {
		t.Fatal("cleanup failure ignored")
	}
	if strings.Join(calls, ",") != "gate,100.96.0.2" {
		t.Fatal(calls)
	}
	fail = false
	calls = nil
	if err := n.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "gate,100.96.0.2,gate" {
		t.Fatal("cleanup not retried", calls)
	}
}
func TestReplacedAddressIsClosedUntilConntrackCleanup(t *testing.T) {
	l := model.Lease{ID: "old", IP: "100.96.0.2", ExpiresAt: time.Now().Add(time.Minute)}
	stage := 0
	n := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16", Run: func(_ context.Context, s string) error {
		if stage == 1 && strings.Contains(s, "add element inet netgo active4 { 100.96.0.2") {
			t.Fatal("new lease opened before cleanup")
		}
		return nil
	}, Conntrack: func(context.Context, string) error {
		if stage == 1 {
			stage = 2
		}
		return nil
	}}
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil {
		t.Fatal(err)
	}
	stage = 1
	l.ID = "new"
	if err := n.Apply(context.Background(), []model.Lease{l}); err != nil || stage != 2 {
		t.Fatal(err, stage)
	}
}
func TestFirewallOwnershipAndOffload(t *testing.T) {
	for _, rules := range []string{
		`{"nftables":[{"table":{"family":"inet","name":"netgo"}}]}`,
		`{"nftables":[{"flowtable":{"dev":["eth0","wan0"]}}]}`,
	} {
		if err := ValidateRuleset([]byte(rules), "wt0", "eth0"); err == nil {
			t.Fatal("unsafe firewall accepted", rules)
		}
	}
	if err := ValidateRuleset([]byte(`{"nftables":[{"table":{"family":"inet","name":"netgo","comment":"NetGo lease gate v1"}},{"flowtable":{"dev":["wan0"]}}]}`), "wt0", "eth0"); err != nil {
		t.Fatal(err)
	}
}
func TestConntrackEmptyScopeDoesNotMaskPermissionFailure(t *testing.T) {
	if !noConntrackEntries([]byte("conntrack v1.4.8 (conntrack-tools): 0 flow entries have been deleted.\n")) {
		t.Fatal("empty scope rejected")
	}
	for _, message := range []string{"Operation failed: you must be root", "Operation failed\nconntrack v1.4.8: 0 flow entries have been deleted.\n"} {
		if noConntrackEntries([]byte(message)) {
			t.Fatal("cleanup failure masked", message)
		}
	}
}
func TestRejectInjectionIPv6AndOverlongTTL(t *testing.T) {
	now := time.Now()
	if _, e := Render("wt0\";flush ruleset", "eth0", "100.96.0.0/16", nil, now); e == nil {
		t.Fatal("interface injection accepted")
	}
	for _, l := range []model.Lease{{IP: "2001:db8::1", ExpiresAt: now.Add(time.Minute)}, {IP: "100.96.0.2", ExpiresAt: now.Add(121 * time.Second)}} {
		if _, e := Render("wt0", "eth0", "100.96.0.0/16", []model.Lease{l}, now); e == nil {
			t.Fatal("invalid lease accepted")
		}
	}
}
