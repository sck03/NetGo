package site

import (
	"errors"
	"fmt"
	"net"
	"net/url"
)

// WithDirectEndpoints changes dial targets without changing the TLS/OIDC
// identity or auxiliary tunnels. Existing endpoint IDs remain stable.
func (p Profile) WithDirectEndpoints(addresses []string) (Profile, error) {
	next := p
	next.Candidates = nil
	used := map[string]bool{}
	for _, c := range p.Candidates {
		used[c.ID] = true
	}
	seen := map[string]bool{}
	for _, address := range addresses {
		if seen[address] {
			return p, errors.New("duplicate direct endpoint")
		}
		seen[address] = true
		candidate := Candidate{Kind: "direct", Address: address}
		for _, old := range p.Candidates {
			if old.Kind == "direct" && old.Address == address {
				candidate = old
				break
			}
		}
		if candidate.ID == "" {
			for i := 1; ; i++ {
				id := fmt.Sprintf("direct-%d", i)
				if !used[id] {
					candidate.ID = id
					used[id] = true
					break
				}
			}
		}
		next.Candidates = append(next.Candidates, candidate)
	}
	for _, c := range p.Candidates {
		if c.Kind == "auxiliary" {
			next.Candidates = append(next.Candidates, c)
		}
	}
	if err := next.validate(false); err != nil {
		return p, err
	}
	return next, nil
}

// ChangeOrigin updates only candidates that actually used the former origin.
// Independent WAN and LAN targets must not be rewritten to the same address.
func (p Profile) ChangeOrigin(origin string) Profile {
	next := p
	next.Origin = origin
	next.Candidates = append([]Candidate(nil), p.Candidates...)
	oldURL, oldErr := url.Parse(p.Origin)
	newURL, newErr := url.Parse(origin)
	if oldErr != nil || newErr != nil {
		return next
	}
	authority := func(u *url.URL) string {
		port := u.Port()
		if port == "" {
			port = "443"
		}
		return net.JoinHostPort(u.Hostname(), port)
	}
	for i, c := range next.Candidates {
		if c.Kind == "direct" && c.Address == authority(oldURL) {
			next.Candidates[i].Address = authority(newURL)
		}
	}
	return next
}
