package client

import (
	"netgo/internal/model"
	"testing"
)

func TestInternetRequiresPermissionAndExplicitChoice(t *testing.T) {
	lan := model.Resource{ID: "nas", Name: "NAS", CIDR: "192.168.18.20/32", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}
	internet := model.Resource{ID: "internet", Name: "Internet", CIDR: "0.0.0.0/0", Protocol: "internet", Enabled: true}
	granted := []model.Resource{lan, internet}
	selected, err := SelectedResources(granted, false)
	if err != nil || len(selected) != 1 || selected[0].ID != "nas" {
		t.Fatalf("default installed Internet route: %v %v", selected, err)
	}
	selected, err = SelectedResources(granted, true)
	if err != nil || len(selected) != 2 {
		t.Fatalf("authorized choice unavailable: %v %v", selected, err)
	}
	if _, err = SelectedResources([]model.Resource{lan}, true); err == nil {
		t.Fatal("client choice escalated permission")
	}
	internet.Enabled = false
	if _, err = SelectedResources([]model.Resource{lan, internet}, true); err == nil {
		t.Fatal("disabled permission allowed Internet")
	}
	internet.Enabled = true
	internet.CIDR = "192.168.18.0/24"
	if _, err = SelectedResources([]model.Resource{internet}, true); err == nil {
		t.Fatal("Internet type bypassed LAN validation")
	}
}
