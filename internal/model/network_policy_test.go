package model

import (
	"testing"
	"time"
)

func TestNetworkPolicyIgnoresDisplayAndTracksAuthority(t *testing.T) {
	before := NewState()
	before.Users["u"] = User{ID: "u", Resources: []string{"r"}}
	before.Resources["r"] = Resource{ID: "r", CIDR: "192.168.1.0/24", Protocol: "tcp", Ports: []uint16{443}, Enabled: true}
	after := before.Clone()
	u := after.Users["u"]
	u.Name = "new"
	u.Email = "new@test"
	u.EditRevision++
	u.AuthRevision++
	u.CleanupAttempts++
	after.Users["u"] = u
	r := after.Resources["r"]
	r.Name = "new"
	r.EditToken = "new"
	after.Resources["r"] = r
	if NetworkPolicyChanged(&before, &after) {
		t.Fatal("display edit triggered network reconciliation")
	}
	for name, change := range map[string]func(*State){
		"disabled":  func(s *State) { u := s.Users["u"]; u.Disabled = true; s.Users["u"] = u },
		"expiry":    func(s *State) { u := s.Users["u"]; u.ExpiresAt = time.Now(); s.Users["u"] = u },
		"resources": func(s *State) { u := s.Users["u"]; u.Resources = nil; s.Users["u"] = u },
		"port":      func(s *State) { r := s.Resources["r"]; r.Ports[0] = 80; s.Resources["r"] = r },
		"deletion":  func(s *State) { delete(s.Users, "u") },
	} {
		t.Run(name, func(t *testing.T) {
			after := before.Clone()
			change(&after)
			if !NetworkPolicyChanged(&before, &after) {
				t.Fatal("authority change missed")
			}
		})
	}
}
