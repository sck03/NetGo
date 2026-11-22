package netbird

import "testing"

func TestNetGoRelayEndpointIdentity(t *testing.T) {
	for _, c := range []struct {
		actual, expected string
		match            bool
	}{
		{"rels://relay.example:18443", "relay.example", true},
		{"relay.example:18443", "relay.example", true},
		{"rels://RELAY.example:18443", "relay.example", true},
		{"rels://relay.example.evil:18443", "relay.example", false},
		{"rels://evil.example/relay.example", "relay.example", false},
		{"rels://relay.example@evil.example:18443", "relay.example", false},
		{"rels://relay.aux:28443", "rels://relay.aux:28443", true},
		{"rels://relay.aux:443", "rels://relay.aux:28443", false},
		{"rels://relay.aux:28443", "", false},
	} {
		if got := relayMatches(c.actual, c.expected); got != c.match {
			t.Errorf("endpoint %q expected %q: got %v", c.actual, c.expected, got)
		}
	}
}
