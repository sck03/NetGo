package site

import "testing"

func TestCustomDeploymentPortsAnd443Constraints(t *testing.T) {
	if err := (Deployment{Mode: "diagnostic", LocalRelayPort: 18443, LocalSTUNPort: 13478}).Validate("https://control.example:10443"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []Deployment{{Mode: "other"}, {LocalRelayPort: -1}, {LocalSTUNPort: 65536}, {Mode: "443", LocalRelayPort: 18443}} {
		if d.Validate("https://control.example") == nil {
			t.Fatal("invalid deployment accepted", d)
		}
	}
	if (Deployment{Mode: "443"}).Validate("https://control.example:10443") == nil {
		t.Fatal("non-443 control authority accepted in compatibility mode")
	}
}

func TestAuxiliaryPorts(t *testing.T) {
	base := Auxiliary{ID: "aux", Host: "aux.example", Relay: "rels://relay.aux.example:28443", STUN: "stun:relay.aux.example:23478", TunnelHost: "tunnel.aux.example", TokenFile: "/etc/netgo/frp.secret", Mode: "diagnostic", ControlPort: 24444, TunnelPort: 27000}
	if err := base.Validate(); err != nil || base.PublicControlPort() != 24444 || base.PublicTunnelPort() != 27000 {
		t.Fatal("custom auxiliary ports rejected", err)
	}
	changes := []func(*Auxiliary){
		func(a *Auxiliary) { a.Mode = "443" },
		func(a *Auxiliary) { a.ControlPort = 28443 },
		func(a *Auxiliary) { a.TunnelPort = 65536 },
		func(a *Auxiliary) { a.Relay = "http://relay.aux.example:443" },
		func(a *Auxiliary) { a.STUN = "stun:relay.aux.example:28443" },
		func(a *Auxiliary) { a.TunnelHost = "bad.example\ncommand" },
	}
	for i, change := range changes {
		a := base
		change(&a)
		if a.Validate() == nil {
			t.Fatal("invalid auxiliary accepted", i)
		}
	}
}
