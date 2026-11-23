package model

import (
	"testing"
	"time"
)

func remoteRouteFixture() State {
	s := NewState()
	s.Users["c"] = User{ID: "c"}
	s.Users["h"] = User{ID: "h"}
	s.Devices["c"] = Device{ID: "c", UserID: "c", PeerID: "pc"}
	s.Devices["h"] = Device{ID: "h", UserID: "h", PeerID: "ph"}
	s.Hosts["h"] = Host{ID: "h", Enabled: true, Boot: "boot", Port: 29990}
	s.RemoteGrants["g"] = RemoteGrant{ID: "g", UserID: "c", HostID: "h", Revision: 3}
	s.Credentials["auth"] = Credential{UserID: "c", DeviceID: "c"}
	s.Leases["lc"] = Lease{DeviceID: "c", PeerID: "pc"}
	s.Leases["lh"] = Lease{DeviceID: "h", PeerID: "ph"}
	s.RemoteSessions["s"] = RemoteSession{ID: "s", UserID: "c", ControllerID: "c", HostID: "h", HostBoot: "boot", GrantID: "g", PolicyRevision: 3, ControllerAuth: "auth", ControllerLease: "lc", HostLease: "lh", State: "issued"}
	return s
}

func TestRemoteRouteClosesOnOwnershipAndPolicyChanges(t *testing.T) {
	for name, change := range map[string]func(*State){
		"closed":         func(s *State) { r := s.RemoteSessions["s"]; r.State = "closed"; s.RemoteSessions["s"] = r },
		"logout":         func(s *State) { delete(s.Credentials, "auth") },
		"lease removed":  func(s *State) { delete(s.Leases, "lh") },
		"host reboot":    func(s *State) { h := s.Hosts["h"]; h.Boot = "new"; s.Hosts["h"] = h },
		"disabled owner": func(s *State) { u := s.Users["h"]; u.Disabled = true; s.Users["h"] = u },
		"peer changed":   func(s *State) { d := s.Devices["h"]; d.PeerID = "other"; s.Devices["h"] = d },
		"grant removed":  func(s *State) { delete(s.RemoteGrants, "g") },
		"grant revised":  func(s *State) { g := s.RemoteGrants["g"]; g.Revision++; s.RemoteGrants["g"] = g },
	} {
		t.Run(name, func(t *testing.T) {
			s := remoteRouteFixture()
			if len(RemoteRoutes(s)) != 1 {
				t.Fatal("fixture")
			}
			change(&s)
			if len(RemoteRoutes(s)) != 0 {
				t.Fatal("stale peer access retained")
			}
		})
	}
}

func TestRemoteRenewalDoesNotChurnNetworkPolicy(t *testing.T) {
	s := remoteRouteFixture()
	before := RemoteRoutes(s)
	r := s.RemoteSessions["s"]
	r.State = "active"
	r.Sequence = 12
	r.ExpiresAt = time.Now().Add(time.Minute)
	s.RemoteSessions["s"] = r
	after := RemoteRoutes(s)
	if len(after) != 1 || before[0] != after[0] || after[0].ControllerPeer != "pc" || after[0].HostPeer != "ph" || after[0].Port != 29990 {
		t.Fatal(after)
	}
}
