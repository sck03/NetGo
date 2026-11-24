package gateway

import (
	"context"
	"netgo/internal/model"
	"netgo/internal/site"
	"strings"
	"testing"
	"time"
)

func TestInternetGateIncludesOnlyAuthorizedUnexpiredSources(t *testing.T) {
	now := time.Now()
	resource := model.Resource{ID: "internet", Name: "Internet", CIDR: "0.0.0.0/0", Protocol: "internet", Enabled: true}
	bypass := site.Bypass{Enabled: true, Gateway: "192.168.18.1", DNS: []string{"192.168.18.1"}}
	leases := []model.Lease{
		{ID: "permitted", IP: "100.96.0.2", ExpiresAt: now.Add(time.Minute), Resources: []model.Resource{resource}},
		{ID: "unpermitted", IP: "100.96.0.3", ExpiresAt: now.Add(time.Minute)},
		{ID: "expired", IP: "100.96.0.4", ExpiresAt: now, Resources: []model.Resource{resource}},
	}
	script, err := RenderBypass("wt0", "eth0", "100.96.0.0/16", []string{"192.168.18.0/24"}, bypass, leases, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "resources ip saddr 100.96.0.2 jump internet") {
		t.Fatal("authorized source missing")
	}
	for _, ip := range []string{"100.96.0.3", "100.96.0.4"} {
		if strings.Contains(script, "resources ip saddr "+ip+" jump internet") {
			t.Fatal("unauthorized Internet source", ip)
		}
	}
	if strings.Index(script, "internet ip daddr @nonpublic4 return") > strings.Index(script, "internet meta l4proto") {
		t.Fatal("Internet allowed before private-address exclusion")
	}
	if !strings.Contains(script, "internet ip daddr 192.168.18.1 meta l4proto { tcp, udp } th dport 53") {
		t.Fatal("explicit DNS permission missing")
	}
	disabled, err := Render("wt0", "eth0", "100.96.0.0/16", leases, now)
	if err != nil || strings.Contains(disabled, "jump internet") {
		t.Fatal("disabled bypass forwarded Internet", err)
	}
}

func TestRevokingInternetClosesBothGateAndNATBeforeConntrackCleanup(t *testing.T) {
	resource := model.Resource{ID: "internet", Name: "Internet", CIDR: "0.0.0.0/0", Protocol: "internet", Enabled: true}
	lease := model.Lease{ID: "same", IP: "100.96.0.2", ExpiresAt: time.Now().Add(time.Minute), Resources: []model.Resource{resource}}
	revoked := false
	closed := false
	cleaned := false
	gate := &NFT{Interface: "wt0", LANInterface: "eth0", Overlay: "100.96.0.0/16", LocalCIDRs: []string{"192.168.18.0/24"}, Bypass: site.Bypass{Enabled: true, Gateway: "192.168.18.1", DNS: []string{"192.168.18.1"}},
		Run: func(_ context.Context, s string) error {
			if revoked {
				if strings.Contains(s, "resources ip saddr 100.96.0.2 jump internet") || strings.Contains(s, "internet4 { 100.96.0.2 timeout") {
					t.Fatal("revoked Internet gate or NAT retained")
				}
				closed = true
			}
			return nil
		},
		Conntrack: func(context.Context, string) error {
			if revoked {
				if !closed {
					t.Fatal("conntrack cleaned before closing Internet")
				}
				cleaned = true
			}
			return nil
		}}
	if err := gate.Apply(context.Background(), []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
	revoked = true
	lease.Resources = nil
	if err := gate.Apply(context.Background(), []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("revoked tracked Internet connection survived")
	}
}
