package site

import (
	"netgo/internal/model"
	"strings"
	"testing"
)

func TestStrictDecodeAndUnsafeRoutes(t *testing.T) {
	var p Profile
	if Decode(strings.NewReader("{\"unknown\":1}"), &p) == nil {
		t.Fatal("unknown fields accepted")
	}
	if Decode(strings.NewReader("{} {}"), &p) == nil {
		t.Fatal("trailing JSON accepted")
	}
	for _, cidr := range []string{"0.0.0.0/0", "100.96.0.0/16", "2001:db8::/64", "192.168.1.1/24"} {
		r := model.Resource{ID: "r", Name: "r", CIDR: cidr, Protocol: "tcp", Ports: []uint16{443}}
		if ValidateResource(r, "100.96.0.0/16") == nil {
			t.Fatal("unsafe resource", cidr)
		}
	}
}
func TestOversizedJSONAndNamedCandidatePortsRejected(t *testing.T) {
	var p Profile
	if Decode(strings.NewReader("{}"+strings.Repeat(" ", 1<<20)), &p) == nil {
		t.Fatal("oversized JSON tail silently ignored")
	}
	p = Profile{Protocol: 2, ID: "test", Origin: "https://site.test", Candidates: []Candidate{{ID: "direct", Kind: "direct", Address: "site.test:https"}}}
	if p.Validate() == nil {
		t.Fatal("Go exported a candidate the native client cannot accept")
	}
	p.Candidates[0].Address = "site.test:443"
	p.Origin = "https://site.test:0"
	if p.Validate() == nil {
		t.Fatal("invalid canonical port accepted")
	}
}
func TestStandaloneCandidateRequired(t *testing.T) {
	p := Profile{Protocol: 2, ID: "site", Origin: "https://site.test", Candidates: []Candidate{{ID: "vps", Kind: "auxiliary", Address: "vps.test:443"}}}
	if p.Validate() == nil {
		t.Fatal("VPS-only configuration accepted")
	}
	p.Candidates = append(p.Candidates, Candidate{ID: "local", Kind: "direct", Address: "site.test:443"})
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
}
