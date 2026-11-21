package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"netgo/internal/model"
	"netgo/internal/store"
	"testing"
	"time"
)

type provider struct {
	nonce   string
	calls   int
	blocked bool
}

func (p *provider) Refresh(context.Context, string) (Tokens, error) {
	return Tokens{Subject: "u", Nonce: p.nonce, IDToken: "fresh-id-token", RefreshToken: "next-refresh", Expiry: time.Now().Add(time.Hour)}, nil
}

func TestManagementLoginDoesNotConsumeVPNDeviceQuota(t *testing.T) {
	n, _, _, _, _ := nativeFixture(t)
	_ = n.Store.Update(func(st *model.State) error {
		u := st.Users["u"]
		u.Role = "admin"
		u.MaxDevices = 1
		st.Users["u"] = u
		return nil
	})
	for i := 0; i < 5; i++ {
		public, private, _ := ed25519.GenerateKey(rand.Reader)
		verifier := model.ID()
		begin := Begin{ClientType: "admin", SiteID: "site1", PublicKey: base64.RawURLEncoding.EncodeToString(public), WireGuardKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), Challenge: model.Challenge(verifier)}
		tx, err := n.Begin(begin)
		if err != nil {
			t.Fatal(err)
		}
		sign := func(message string) string {
			return base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(message)))
		}
		if err = n.Complete(context.Background(), tx.ID, "alice", "correct-password", sign(Proof(tx))); err != nil {
			t.Fatal(err)
		}
		result, err := n.Exchange(context.Background(), tx.ID, verifier, sign(Proof(tx)+"\nexchange\n"+verifier))
		if err != nil {
			t.Fatal("management quota", err)
		}
		credential := model.Credential{UserID: "u", DeviceID: result.DeviceID, ExpiresAt: result.ExpiresAt}
		refreshed, err := n.Refresh(context.Background(), credential, result.Token, "refresh")
		if err != nil || refreshed.IDToken != "fresh-id-token" {
			t.Fatal("refresh did not propagate core identity token", err)
		}
	}
	state, err := n.Store.View()
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range state.Devices {
		if device.ClientType != "admin" {
			t.Fatal("management key became a VPN device")
		}
	}
}

func TestOrdinaryAccountCannotClaimManagementLogin(t *testing.T) {
	n, private, verifier, begin, _ := nativeFixture(t)
	begin.ClientType = "admin"
	tx, err := n.Begin(begin)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(message string) string {
		return base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(message)))
	}
	if err = n.Complete(context.Background(), tx.ID, "alice", "correct-password", sign(Proof(tx))); err != nil {
		t.Fatal(err)
	}
	if _, err = n.Exchange(context.Background(), tx.ID, verifier, sign(Proof(tx)+"\nexchange\n"+verifier)); !errors.Is(err, model.ErrForbidden) {
		t.Fatal("ordinary user claimed management scope", err)
	}
}

func (p *provider) Authorize(_ context.Context, email, password, nonce, challenge string) (string, error) {
	p.calls++
	if password != "correct-password" {
		return "", model.ErrUnauthorized
	}
	p.nonce = nonce
	return "code", nil
}
func (p *provider) Exchange(_ context.Context, code, verifier string) (Tokens, error) {
	return Tokens{Subject: "u", Nonce: p.nonce, IDToken: "trusted-test-id-token", Expiry: time.Now().Add(time.Hour)}, nil
}
func (p *provider) UserActive(context.Context, string) error {
	if p.blocked {
		return errors.New("blocked")
	}
	return nil
}
func nativeFixture(t *testing.T) (*Native, ed25519.PrivateKey, string, Begin, *provider) {
	t.Helper()
	st, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.Update(func(s *model.State) error {
		s.Users["u"] = model.User{ID: "u", Alias: "alice", Email: "alice@example.test", MaxDevices: 2, Role: "user"}
		return nil
	})
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	verifier := model.ID()
	p := &provider{}
	n := &Native{SiteID: "site1", Store: st, Provider: p}
	b := Begin{SiteID: "site1", PublicKey: base64.RawURLEncoding.EncodeToString(pub), WireGuardKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), Challenge: model.Challenge(verifier)}
	return n, priv, verifier, b, p
}
func TestNativeOneTimePKCEAndDeviceProof(t *testing.T) {
	n, priv, verifier, b, _ := nativeFixture(t)
	tx, e := n.Begin(b)
	if e != nil {
		t.Fatal(e)
	}
	sign := func(s string) string { return base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(s))) }
	if e = n.Complete(context.Background(), tx.ID, "alice", "correct-password", sign(Proof(tx))); e != nil {
		t.Fatal(e)
	}
	if _, e = n.Exchange(context.Background(), tx.ID, "wrong", sign("wrong")); e == nil {
		t.Fatal("invalid PKCE accepted")
	}
	sig := sign(Proof(tx) + "\nexchange\n" + verifier)
	result, e := n.Exchange(context.Background(), tx.ID, verifier, sig)
	if e != nil || result.User.ID != "u" {
		t.Fatal(e)
	}
	if _, e = n.Exchange(context.Background(), tx.ID, verifier, sig); e == nil {
		t.Fatal("authorization replay accepted")
	}
	st, _ := n.Store.View()
	if _, ok := st.Credentials[model.Hash(result.Token)]; !ok {
		t.Fatal("credential not persisted")
	}
	for k := range st.Credentials {
		if k == result.Token {
			t.Fatal("raw bearer token stored")
		}
	}
}
func TestInvalidSignatureNeverChecksPassword(t *testing.T) {
	n, _, _, b, p := nativeFixture(t)
	tx, _ := n.Begin(b)
	if e := n.Complete(context.Background(), tx.ID, "alice", "correct-password", "invalid"); e == nil {
		t.Fatal("forged proof accepted")
	}
	if p.calls != 0 {
		t.Fatal("password verifier invoked for forged device")
	}
}
func TestBlockedUserCannotExchange(t *testing.T) {
	n, priv, v, b, p := nativeFixture(t)
	tx, _ := n.Begin(b)
	sign := func(s string) string { return base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(s))) }
	if e := n.Complete(context.Background(), tx.ID, "alice", "correct-password", sign(Proof(tx))); e != nil {
		t.Fatal(e)
	}
	p.blocked = true
	if _, e := n.Exchange(context.Background(), tx.ID, v, sign(Proof(tx)+"\nexchange\n"+v)); e == nil {
		t.Fatal("disabled user exchanged authorization")
	}
}
