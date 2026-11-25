package site

import "testing"

func TestEndpointIdentityAndIndependentWANs(t *testing.T) {
	p := Profile{Protocol: 2, ID: "site", Origin: "https://192.168.18.220:10443", Candidates: []Candidate{
		{ID: "lan", Kind: "direct", Address: "192.168.18.220:10443"},
		{ID: "wan", Kind: "direct", Address: "wan.example:20443"},
		{ID: "vps", Kind: "auxiliary", Address: "vps.example:24443"},
	}}
	next, err := p.WithDirectEndpoints([]string{"192.168.18.220:10443", "wan.example:20443", "wan2.example:30443"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Origin != p.Origin || next.Candidates[0].ID != "lan" || next.Candidates[1].ID != "wan" || next.Candidates[3] != p.Candidates[2] {
		t.Fatal("endpoint identity lost")
	}
	changed := next.ChangeOrigin("https://control.example:10443")
	if changed.Candidates[0].Address != "control.example:10443" || changed.Candidates[1] != next.Candidates[1] || changed.Candidates[2] != next.Candidates[2] || changed.Candidates[3] != next.Candidates[3] {
		t.Fatal("independent endpoint overwritten")
	}
	if p.Candidates[0].Address != "192.168.18.220:10443" {
		t.Fatal("original mutated")
	}
	for _, addresses := range [][]string{nil, {"a:443", "a:443"}, {"https://host:443"}, {"host:0"}} {
		if _, err := p.WithDirectEndpoints(addresses); err == nil {
			t.Fatalf("accepted invalid endpoints: %v", addresses)
		}
	}
}
