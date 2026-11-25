package node

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestDNSProbeChecksEveryResolver(t *testing.T) {
	lookup := func(ctx context.Context, server string) ([]netip.Addr, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing DNS deadline")
		}
		switch server {
		case "192.168.1.1":
			return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
		case "192.168.1.2":
			return []netip.Addr{netip.MustParseAddr("198.18.0.1")}, nil
		default:
			return nil, context.DeadlineExceeded
		}
	}
	results, err := probeDNS(context.Background(), []string{"192.168.1.1", "192.168.1.2"}, lookup)
	if err != nil || results[0].FakeIP || !results[1].FakeIP {
		t.Fatalf("DNS mode: %+v %v", results, err)
	}
	results, err = probeDNS(context.Background(), []string{"192.168.1.2", "192.168.1.3"}, lookup)
	if !errors.Is(err, context.DeadlineExceeded) || !results[0].FakeIP {
		t.Fatalf("failure lost: %+v %v", results, err)
	}
	_, err = probeDNS(context.Background(), []string{"192.168.1.1"}, func(context.Context, string) ([]netip.Addr, error) { return nil, nil })
	if err == nil {
		t.Fatal("empty answer classified as ordinary DNS")
	}
	_, err = probeDNS(context.Background(), []string{"192.168.1.1", "192.168.1.1"}, lookup)
	if err == nil {
		t.Fatal("duplicate resolvers accepted")
	}
}

func TestBypassDraftPreservesConfigurationOnDNSFailure(t *testing.T) {
	c := configFixture(t)
	c.LocalCIDRs = []string{"192.168.18.0/24"}
	before := c
	term, _ := testTerminal("1\n192.168.18.1\n192.168.18.1\n\n1\n")
	got, err := term.editBypass(c, func(context.Context, string) ([]netip.Addr, error) { return nil, context.DeadlineExceeded })
	if err == nil || !strings.Contains(err.Error(), "配置未保存") || !reflect.DeepEqual(got, before) {
		t.Fatalf("failure changed draft: %v", err)
	}
	term, _ = testTerminal("1\n192.168.18.1\n192.168.18.1\n192.168.18.2\n1\n")
	got, err = term.editBypass(c, func(_ context.Context, server string) ([]netip.Addr, error) {
		ip := "93.184.215.14"
		if server == "192.168.18.2" {
			ip = "198.18.0.3"
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	})
	if err != nil || got.Bypass.FakeIPCIDR != "198.18.0.0/15" || !reflect.DeepEqual(got.Site.InternetDNS, got.Bypass.DNS) {
		t.Fatalf("fallback mode lost: %+v %v", got.Bypass, err)
	}
	if !reflect.DeepEqual(c, before) {
		t.Fatal("original mutated")
	}
	term, _ = testTerminal("1\n\n\n\n2\n")
	again, err := term.editBypass(got, func(context.Context, string) ([]netip.Addr, error) {
		t.Fatal("manual mode queried DNS")
		return nil, nil
	})
	if err != nil || !reflect.DeepEqual(again.Bypass.DNS, got.Bypass.DNS) || again.Bypass.FakeIPCIDR != "" {
		t.Fatalf("existing DNS lost: %+v %v", again.Bypass, err)
	}
}
