package identity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coreos/go-oidc/v3/oidc"
	"io"
	"net/http"
	"net/url"
	"netgo/internal/model"
	"netgo/internal/store"
	"strings"
	"sync"
	"time"
)

type Tokens struct {
	IDToken        string `json:"id_token"`
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token"`
	ExpiresIn      int    `json:"expires_in"`
	Subject, Nonce string
	Expiry         time.Time
}
type Provider interface {
	Authorize(context.Context, string, string, string, string) (string, error)
	Exchange(context.Context, string, string) (Tokens, error)
	UserActive(context.Context, string) error
}
type Dex struct {
	Origin, LocalURL, Secret string
	Client                   *http.Client
	CheckUser                func(context.Context, string) error
}

func (d *Dex) Authorize(ctx context.Context, email, password, nonce, challenge string) (string, error) {
	b, _ := json.Marshal(map[string]string{"username": email, "password": password, "nonce": nonce, "challenge": challenge})
	req, e := http.NewRequestWithContext(ctx, "POST", d.LocalURL+"/oauth2/netgo/native", bytes.NewReader(b))
	if e != nil {
		return "", e
	}
	req.Header.Set("Authorization", "Bearer "+d.Secret)
	req.Header.Set("Content-Type", "application/json")
	r, e := d.Client.Do(req)
	if e != nil {
		return "", errors.New("identity provider unavailable")
	}
	defer r.Body.Close()
	var v struct {
		Code string `json:"code"`
	}
	if r.StatusCode != 200 || json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&v) != nil || v.Code == "" {
		return "", model.ErrUnauthorized
	}
	return v.Code, nil
}
func (d *Dex) Exchange(ctx context.Context, code, verifier string) (Tokens, error) {
	f := url.Values{"grant_type": {"authorization_code"}, "client_id": {"netbird-cli"}, "redirect_uri": {"http://localhost:53000/"}, "code": {code}, "code_verifier": {verifier}}
	return d.token(ctx, f)
}
func (d *Dex) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	return d.token(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {"netbird-cli"}, "refresh_token": {refresh}})
}
func (d *Dex) token(ctx context.Context, f url.Values) (Tokens, error) {
	var t Tokens
	req, _ := http.NewRequestWithContext(ctx, "POST", d.LocalURL+"/oauth2/token", strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, e := d.Client.Do(req)
	if e != nil {
		return t, errors.New("identity provider unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 || json.NewDecoder(io.LimitReader(r.Body, 32768)).Decode(&t) != nil {
		return t, model.ErrUnauthorized
	}
	// JWKS is fetched locally, while issuer/audience validation uses the canonical identity.
	ctx = oidc.ClientContext(ctx, d.Client)
	keys := oidc.NewRemoteKeySet(ctx, d.LocalURL+"/oauth2/keys")
	v := oidc.NewVerifier(d.Origin+"/oauth2", keys, &oidc.Config{ClientID: "netbird-cli"})
	id, e := v.Verify(ctx, t.IDToken)
	if e != nil {
		return Tokens{}, model.ErrUnauthorized
	}
	var c struct {
		Nonce string `json:"nonce"`
	}
	if e = id.Claims(&c); e != nil {
		return Tokens{}, e
	}
	t.Subject = id.Subject
	t.Expiry = id.Expiry
	t.Nonce = c.Nonce
	return t, nil
}
func (d *Dex) UserActive(ctx context.Context, id string) error {
	if d.CheckUser == nil {
		return errors.New("user verifier unavailable")
	}
	return d.CheckUser(ctx, id)
}

type Begin struct {
	ClientType   string `json:"client_type,omitempty"`
	SiteID       string `json:"site_id"`
	PublicKey    string `json:"public_key"`
	WireGuardKey string `json:"wireguard_key"`
	Challenge    string `json:"challenge"`
}
type Transaction struct {
	ID           string    `json:"id"`
	Nonce        string    `json:"nonce"`
	ExpiresAt    time.Time `json:"expires_at"`
	Begin        Begin     `json:"-"`
	Code, UserID string    `json:"-"`
	Completed    bool      `json:"-"`
	AuthRevision uint64    `json:"-"`
}
type Result struct {
	Token        string     `json:"token"`
	IDToken      string     `json:"id_token"`
	ExpiresAt    time.Time  `json:"expires_at"`
	User         model.User `json:"user"`
	DeviceID     string     `json:"device_id"`
	RefreshToken string     `json:"refresh_token,omitempty"`
}
type Native struct {
	mu       sync.Mutex
	SiteID   string
	Store    *store.Store
	Provider Provider
	tx       map[string]Transaction
	attempts map[string][]time.Time
}

func (n *Native) Begin(b Begin) (Transaction, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var z Transaction
	if b.SiteID != n.SiteID || (b.ClientType != "" && b.ClientType != "admin") {
		return z, model.ErrForbidden
	}
	pk, e := base64.RawURLEncoding.DecodeString(b.PublicKey)
	wk, we := base64.StdEncoding.DecodeString(b.WireGuardKey)
	ch, ce := base64.RawURLEncoding.DecodeString(b.Challenge)
	if e != nil || we != nil || ce != nil || len(pk) != 32 || len(wk) != 32 || len(ch) != 32 {
		return z, errors.New("invalid public key/PKCE challenge")
	}
	if n.tx == nil {
		n.tx = map[string]Transaction{}
	}
	for id, t := range n.tx {
		if time.Now().After(t.ExpiresAt) {
			delete(n.tx, id)
		}
	}
	if len(n.tx) >= 2048 {
		return z, errors.New("authentication capacity reached")
	}
	t := Transaction{ID: model.ID(), Nonce: "netgo:" + b.PublicKey + ":" + b.WireGuardKey + ":" + model.ID(), ExpiresAt: time.Now().Add(time.Minute), Begin: b}
	n.tx[t.ID] = t
	return t, nil
}
func Proof(t Transaction) string {
	return "netgo-native-v1\n" + t.Begin.SiteID + "\n" + t.ID + "\n" + t.Nonce
}
func Verify(key, message, signature string) bool {
	pk, e := base64.RawURLEncoding.DecodeString(key)
	sig, se := base64.RawURLEncoding.DecodeString(signature)
	return e == nil && se == nil && len(pk) == 32 && ed25519.Verify(pk, []byte(message), sig)
}
func (n *Native) Complete(ctx context.Context, id, alias, password, signature string) error {
	n.mu.Lock()
	t, ok := n.tx[id]
	if !ok || t.Completed || time.Now().After(t.ExpiresAt) || !Verify(t.Begin.PublicKey, Proof(t), signature) {
		n.mu.Unlock()
		return model.ErrUnauthorized
	}
	t.Completed = true
	n.tx[id] = t
	if n.attempts == nil {
		n.attempts = map[string][]time.Time{}
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	a := []time.Time{}
	for _, v := range n.attempts[alias] {
		if time.Since(v) < time.Minute {
			a = append(a, v)
		}
	}
	if len(a) >= 5 {
		n.mu.Unlock()
		return errors.New("rate_limited")
	}
	attemptedAt := time.Now()
	n.attempts[alias] = append(a, attemptedAt)
	if len(n.attempts) > 4096 {
		for k, v := range n.attempts {
			if len(v) == 0 || time.Since(v[len(v)-1]) > time.Minute {
				delete(n.attempts, k)
			}
		}
	}
	n.mu.Unlock()
	u, e := n.Store.UserByAlias(alias)
	if e != nil {
		return e
	}
	if u.ID == "" || !u.Active(time.Now()) || len(password) > 1024 {
		return model.ErrUnauthorized
	}
	if e = n.Provider.UserActive(ctx, u.ID); e != nil {
		return model.ErrUnauthorized
	}
	code, e := n.Provider.Authorize(ctx, u.Email, password, t.Nonce, t.Begin.Challenge)
	if e != nil {
		return e
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// Successful sign-ins do not consume the account's failed-attempt budget.
	kept := n.attempts[alias][:0]
	for _, at := range n.attempts[alias] {
		if !at.Equal(attemptedAt) {
			kept = append(kept, at)
		}
	}
	n.attempts[alias] = kept
	t.Code = code
	t.UserID = u.ID
	t.AuthRevision = u.AuthRevision
	n.tx[id] = t
	return nil
}
func (n *Native) Exchange(ctx context.Context, id, verifier, signature string) (Result, error) {
	n.mu.Lock()
	t, ok := n.tx[id]
	if !ok || t.Code == "" || time.Now().After(t.ExpiresAt) || len(verifier) < 43 || len(verifier) > 128 || model.Challenge(verifier) != t.Begin.Challenge || !Verify(t.Begin.PublicKey, Proof(t)+"\nexchange\n"+verifier, signature) {
		n.mu.Unlock()
		return Result{}, model.ErrUnauthorized
	}
	delete(n.tx, id)
	n.mu.Unlock()
	tokens, e := n.Provider.Exchange(ctx, t.Code, verifier)
	if e != nil {
		return Result{}, e
	}
	if tokens.Nonce != t.Nonce || tokens.Subject != t.UserID {
		return Result{}, model.ErrForbidden
	}
	if e = n.Provider.UserActive(ctx, t.UserID); e != nil {
		return Result{}, model.ErrForbidden
	}
	out := Result{Token: model.ID(), IDToken: tokens.IDToken, RefreshToken: tokens.RefreshToken, ExpiresAt: tokens.Expiry, DeviceID: model.Hash(n.SiteID + ":" + t.UserID + ":" + t.Begin.PublicKey)}
	e = n.Store.EnrollmentUpdate(t.UserID, func(st *model.State) error {
		u, ok := st.Users[t.UserID]
		if !ok || !u.Active(time.Now()) || u.AuthRevision != t.AuthRevision || !time.Now().Before(out.ExpiresAt) {
			return model.ErrForbidden
		}
		if t.Begin.ClientType == "admin" && u.Role != "admin" {
			return model.ErrForbidden
		}
		if !u.ExpiresAt.IsZero() && u.ExpiresAt.Before(out.ExpiresAt) {
			out.ExpiresAt = u.ExpiresAt
		}
		d, exists := st.Devices[out.DeviceID]
		if exists && (d.DeletePending || d.WireGuardKey != t.Begin.WireGuardKey || d.ClientType != t.Begin.ClientType) {
			return model.ErrForbidden
		}
		if !exists {
			count := 0
			for _, d := range st.Devices {
				if d.UserID == u.ID && d.ClientType != "admin" {
					count++
				}
			}
			if t.Begin.ClientType != "admin" && count >= u.MaxDevices {
				return errors.New("device_limit")
			}
			st.Devices[out.DeviceID] = model.Device{ID: out.DeviceID, UserID: u.ID, PublicKey: t.Begin.PublicKey, WireGuardKey: t.Begin.WireGuardKey, ClientType: t.Begin.ClientType, CreatedAt: time.Now().UTC()}
		}
		st.Credentials[model.Hash(out.Token)] = model.Credential{UserID: u.ID, DeviceID: out.DeviceID, ExpiresAt: out.ExpiresAt}
		st.Record(u.ID, "auth.success", out.DeviceID)
		out.User = u
		return nil
	})
	return out, e
}
func SignatureMessage(method, path, stamp, nonce string, body []byte, token string) string {
	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", method, path, stamp, nonce, model.Hash(string(body)), model.Hash(token))
}
