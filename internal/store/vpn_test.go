package store

import (
	"database/sql"
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

func TestVPNRenewKeepsGateAndRemoteDependenciesWithoutIdleRows(t *testing.T) {
	s, c := renewalFixture(t)
	before, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	var selected model.State
	err = s.VPNRenew(c, func(st *model.State) error {
		selected = *st
		if len(st.Users) != 2 || len(st.Devices) != 2 || len(st.Credentials) != 1 || len(st.Resources) != 1 || len(st.Operations) != 0 {
			t.Fatalf("unrelated data loaded: users=%d devices=%d credentials=%d resources=%d operations=%d", len(st.Users), len(st.Devices), len(st.Credentials), len(st.Resources), len(st.Operations))
		}
		if !reflect.DeepEqual(model.RemoteRoutes(before), model.RemoteRoutes(*st)) || len(model.RemoteRoutes(*st)) != 1 {
			t.Fatal("active route dependency lost")
		}
		l := st.Leases["0"]
		l.ExpiresAt = l.ExpiresAt.Add(time.Minute)
		st.Leases["0"] = l
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

func BenchmarkVPNRenewSnapshot(b *testing.B) {
	s, c := renewalFixture(b)
	old := func(fn func(*model.State) error) error {
		return s.updateWith(func(tx *sql.Tx) (model.State, error) {
			st, err := read(tx, allTables & ^(remoteSessions|remoteOperations))
			if err == nil {
				err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active')")
			}
			return st, err
		}, fn)
	}
	for _, tc := range []struct {
		name   string
		update func(func(*model.State) error) error
	}{
		{"Previous", old}, {"Scoped", func(fn func(*model.State) error) error { return s.VPNRenew(c, fn) }},
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
