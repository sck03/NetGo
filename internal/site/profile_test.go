package site

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSharedProfileUpdateContract(t *testing.T) {
	b, err := os.ReadFile("../../tests/contracts/profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base  map[string]json.RawMessage
		Cases []struct {
			Name      string
			Old, Next map[string]json.RawMessage
			Accept    bool
		}
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			decode := func(patch map[string]json.RawMessage) (Profile, error) {
				values := map[string]json.RawMessage{}
				for k, v := range fixture.Base {
					values[k] = v
				}
				for k, v := range patch {
					values[k] = v
				}
				data, _ := json.Marshal(values)
				var p Profile
				err := Decode(bytes.NewReader(data), &p)
				return p, err
			}
			old, err := decode(tc.Old)
			if err != nil {
				t.Fatal(err)
			}
			next, err := decode(tc.Next)
			if err == nil {
				err = CheckProfileUpdate(old, next)
			}
			if (err == nil) != tc.Accept {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.Accept, err)
			}
		})
	}
}

func TestTrustIdentityIgnoresDiscoveryButBindsAuthority(t *testing.T) {
	p := Profile{Protocol: 2, ID: "site", Origin: "https://SITE.example:10443", CA: "trust"}
	base := TrustIdentity(p)
	next := p
	next.Origin, next.Name, next.ConfigRevision = "https://site.example:443", "Renamed", 4
	if TrustIdentity(next) != base {
		t.Fatal("discovery change lost credentials")
	}
	for _, change := range []func(*Profile){
		func(p *Profile) { p.Origin = "https://other.example" },
		func(p *Profile) { p.ID = "other" },
		func(p *Profile) { p.CA = "another trust" },
		func(p *Profile) { p.Protocol++ },
	} {
		next = p
		change(&next)
		if TrustIdentity(next) == base {
			t.Fatal("trust change retained credentials")
		}
	}
}

func TestProfileNameBoundUsesUTF8Bytes(t *testing.T) {
	p := Profile{Protocol: 2, ID: "site", Name: strings.Repeat("站", 85), Origin: "https://site.example", GatewayIP: "100.96.0.1", Candidates: []Candidate{{ID: "local", Kind: "direct", Address: "site.example:443"}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Name += "站"
	if p.Validate() == nil {
		t.Fatal("oversized UTF-8 name accepted")
	}
}
