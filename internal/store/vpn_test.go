package store

import (
	"errors"
	"fmt"
	"netgo/internal/model"
	"reflect"
	"testing"
	"time"
)

func renewalFixture(t testing.TB) (*Store, model.Credential) {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	credential := model.Credential{TokenHash: "0", UserID: "0", DeviceID: "0", ExpiresAt: time.Now().Add(time.Hour)}
	err = s.Update(func(st *model.State) error {
		for i := 0; i < 200; i++ {
			id := fmt.Sprint(i)
			st.Users[id] = model.User{ID: id, Alias: "u" + id, Resources: []string{id}}
			st.Resources[id] = model.Resource{ID: id, Enabled: true, Protocol: "tcp", CIDR: "192.168.1.1/32", Ports: []uint16{443}}
			st.Devices[id] = model.Device{ID: id, UserID: id, PeerID: "p" + id}
			st.Credentials[id] = model.Credential{UserID: id, DeviceID: id, ExpiresAt: credential.ExpiresAt}
			st.Operations[id] = model.Operation{ExpiresAt: credential.ExpiresAt}
		}
		for _, id := range []string{"0", "1"} {
			st.Leases[id] = model.Lease{ID: id, UserID: id, DeviceID: id, PeerID: "p" + id, ExpiresAt: credential.ExpiresAt}
		}
		st.Hosts["1"] = model.Host{ID: "1", Enabled: true, Revision: 1, Boot: "boot"}
		st.RemoteGrants["g"] = model.RemoteGrant{ID: "g", UserID: "0", HostID: "1", Revision: 1}
		st.RemoteSessions["remote"] = model.RemoteSession{ID: "remote", UserID: "0", ControllerID: "0", HostID: "1", ControllerAuth: "0", ControllerLease: "0", HostLease: "1", HostBoot: "boot", GrantID: "g", PolicyRevision: 1, State: "active"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, credential
}

func TestVPNRenewUpdatesOnlyTargetAndKeepsGateMembership(t *testing.T) {
	s, c := renewalFixture(t)
	before, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	var selected model.State
	err = s.VPNRenew(c, "0", time.Now(), func(st *model.State) error {
		selected = *st
		if len(st.Users) != 1 || len(st.Devices) != 1 || len(st.Leases) != 1 || len(st.Credentials) != 1 || len(st.Resources) != 1 || len(st.Operations) != 0 || len(st.RemoteSessions) != 0 {
			t.Fatalf("unrelated data loaded: users=%d devices=%d credentials=%d resources=%d operations=%d", len(st.Users), len(st.Devices), len(st.Credentials), len(st.Resources), len(st.Operations))
		}
		l := st.Leases["0"]
		l.ExpiresAt = l.ExpiresAt.Add(time.Minute)
		st.Leases["0"] = l
		return nil
	}, func(leases []model.Lease) error {
		if len(leases) != 2 {
			t.Fatal("gate membership lost", leases)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	before.Leases["0"] = selected.Leases["0"]
	if !reflect.DeepEqual(before, after) {
		t.Fatal("renewal mutated unrelated domain data")
	}
}

func TestVPNRenewRollsBackGateFailure(t *testing.T) {
	s, c := renewalFixture(t)
	before, _ := s.View()
	failure := errors.New("gate failed")
	err := s.VPNRenew(c, "0", time.Now(), func(st *model.State) error {
		l := st.Leases["0"]
		l.ExpiresAt = l.ExpiresAt.Add(time.Second)
		st.Leases["0"] = l
		return nil
	}, func([]model.Lease) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	after, _ := s.View()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed renewal committed")
	}
}

func BenchmarkVPNRenewSnapshot(b *testing.B) {
	s, c := renewalFixture(b)
	for _, tc := range []struct {
		name   string
		update func(func(*model.State) error) error
	}{
		{"FullState", s.Update}, {"Targeted", func(fn func(*model.State) error) error {
			return s.VPNRenew(c, "0", time.Now(), fn, func([]model.Lease) error { return nil })
		}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := tc.update(func(*model.State) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkVPNRenewActive(b *testing.B) {
	for _, count := range []int{16, 128, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, err := Open(":memory:")
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			now := time.Now().UTC()
			c := model.Credential{TokenHash: "0", UserID: "0", DeviceID: "0", ExpiresAt: now.Add(time.Hour)}
			err = s.Update(func(st *model.State) error {
				st.Resources["r"] = model.Resource{ID: "r", Enabled: true, Protocol: "tcp", CIDR: "192.168.1.1/32", Ports: []uint16{443}}
				for i := 0; i < count; i++ {
					id := fmt.Sprint(i)
					st.Users[id] = model.User{ID: id, Alias: "u" + id, Resources: []string{"r"}}
					st.Devices[id] = model.Device{ID: id, UserID: id, PeerID: "p" + id}
					st.Leases[id] = model.Lease{ID: id, UserID: id, DeviceID: id, PeerID: "p" + id, ExpiresAt: c.ExpiresAt, Resources: []model.Resource{st.Resources["r"]}}
				}
				st.Credentials["0"] = c
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				err = s.VPNRenew(c, "0", now, func(st *model.State) error {
					lease := st.Leases["0"]
					lease.ExpiresAt = lease.ExpiresAt.Add(time.Nanosecond)
					st.Leases["0"] = lease
					return nil
				}, func([]model.Lease) error { return nil })
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
