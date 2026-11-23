package identity

import (
	"context"
	"encoding/base64"
	"netgo/internal/model"
	"netgo/internal/store"
	"testing"
	"time"
)

func TestRefreshRebindsRemoteIdentityWithoutChangingSignedClaims(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := model.Credential{UserID: "u", DeviceID: "c", ExpiresAt: time.Now().Add(time.Hour)}
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	wg := base64.StdEncoding.EncodeToString(make([]byte, 32))
	expiry := time.Now().Add(30 * time.Second).UTC()
	err = db.Update(func(s *model.State) error {
		s.Users["u"] = model.User{ID: "u"}
		s.Devices["c"] = model.Device{ID: "c", UserID: "u", PublicKey: key, WireGuardKey: wg}
		s.Devices["h"] = model.Device{ID: "h", UserID: "u"}
		s.Hosts["h"] = model.Host{ID: "h", Revision: 1}
		s.Credentials[model.Hash("old")] = c
		for _, state := range []string{"issued", "closed"} {
			s.RemoteSessions[state] = model.RemoteSession{ID: state, State: state, UserID: "u", ControllerID: "c", HostID: "h", ControllerAuth: model.Hash("old"), ExpiresAt: expiry, Sequence: 1}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n := Native{Store: db, Provider: &provider{nonce: "netgo:" + key + ":" + wg + ":nonce"}}
	next, err := n.Refresh(context.Background(), c, "old", "refresh")
	if err != nil {
		t.Fatal(err)
	}
	s, err := db.View()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Credentials[model.Hash("old")]; ok {
		t.Fatal("old token retained")
	}
	issued := s.RemoteSessions["issued"]
	if issued.ControllerAuth != model.Hash(next.Token) || !issued.ExpiresAt.Equal(expiry) || issued.Sequence != 1 || issued.State != "issued" {
		t.Fatal("rotation changed ticket claims")
	}
	if s.RemoteSessions["closed"].ControllerAuth != model.Hash("old") || s.RemoteSessions["closed"].State != "closed" {
		t.Fatal("closed session rebound")
	}
	if _, err = n.Refresh(context.Background(), c, "old", "refresh"); err == nil {
		t.Fatal("old credential replay accepted")
	}
}
