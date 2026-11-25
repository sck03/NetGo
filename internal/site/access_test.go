package site

import (
	"encoding/json"
	"netgo/internal/model"
	"strings"
	"testing"
)

func TestResourceMustFitConfiguredLAN(t *testing.T) {
	c := Config{OverlayCIDR: "100.96.0.0/16", LocalCIDRs: []string{"192.168.18.0/23", "10.1.0.0/24"}}
	for _, tc := range []struct {
		cidr  string
		valid bool
	}{
		{"192.168.19.20/32", true}, {"192.168.18.0/23", true}, {"10.1.0.0/24", true},
		{"192.168.16.0/22", false}, {"192.168.20.1/32", false}, {"8.8.8.8/32", false},
		{"100.96.0.1/32", false}, {"192.168.19.20/24", false},
	} {
		r := model.Resource{ID: "r", Name: "service", CIDR: tc.cidr, Protocol: "tcp", Ports: []uint16{443}, Enabled: true}
		if err := c.ValidateResource(r); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.cidr, err)
		}
	}
	r := model.Resource{ID: "internet", Name: "Internet", CIDR: "0.0.0.0/0", Protocol: "internet", Enabled: true}
	if c.ValidateResource(r) == nil {
		t.Fatal("unconfigured exit enabled")
	}
	r.Enabled = false
	if err := c.ValidateResource(r); err != nil {
		t.Fatal("cannot disable exit", err)
	}
	r.Enabled = true
	c.Bypass.Enabled = true
	if err := c.ValidateResource(r); err != nil {
		t.Fatal(err)
	}
}

func TestAccessSummaryIsIndependentAndLimited(t *testing.T) {
	c := Config{LANInterface: "eth0", LocalCIDRs: []string{"192.168.18.0/23"}, Bypass: Bypass{Enabled: true, Gateway: "192.168.19.254", DNS: []string{"192.168.19.254"}, FakeIPCIDR: "198.18.0.0/15"}}
	a := c.Access()
	a.LocalCIDRs[0] = "changed"
	a.DNS[0] = "changed"
	if c.LocalCIDRs[0] == "changed" || c.Bypass.DNS[0] == "changed" {
		t.Fatal("summary aliases config")
	}
	raw, err := json.Marshal(c.Access())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if len(fields) != 6 || !strings.Contains(string(raw), "fake_ip_cidr") {
		t.Fatal(string(raw))
	}
	empty, _ := json.Marshal((Config{}).Access())
	if strings.Contains(string(empty), "null") {
		t.Fatal("null lists", string(empty))
	}
}

func TestResourceCanBeDisabledAfterLANChanges(t *testing.T) {
	c := Config{OverlayCIDR: "100.96.0.0/16", LocalCIDRs: []string{"10.1.0.0/24"}}
	r := model.Resource{ID: "files", Name: "Files", CIDR: "192.168.18.20/32", Protocol: "tcp", Ports: []uint16{445}}
	if err := c.ValidateResource(r); err != nil {
		t.Fatal("cannot disable old target", err)
	}
	r.Enabled = true
	if c.ValidateResource(r) == nil {
		t.Fatal("old target enabled")
	}
}
