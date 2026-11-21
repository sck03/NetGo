package gateway

import (
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
