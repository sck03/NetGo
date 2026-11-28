package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"netgo/internal/model"
	"netgo/internal/store"
	"testing"
	"time"
)

type unavailableProvider struct {
	provider
	err       error
	refreshed bool
}

func TestJWKSOutageDoesNotRevokeRefresh(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "malformed"}[broken], func(t *testing.T) {
			var origin string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth2/keys" {
					if broken {
						w.Write([]byte("{"))
					} else {
						w.WriteHeader(503)
					}
					return
				}
				claims, _ := json.Marshal(map[string]any{"iss": origin + "/oauth2", "aud": "netbird-cli", "sub": "u", "exp": time.Now().Add(time.Hour).Unix()})
				encode := base64.RawURLEncoding.EncodeToString
				jwt := encode([]byte(`{"alg":"RS256","kid":"test"}`)) + "." + encode(claims) + "." + encode(make([]byte, 256))
				json.NewEncoder(w).Encode(Tokens{IDToken: jwt})
			}))
			defer server.Close()
			origin = server.URL
			d := &Dex{LocalURL: origin, Origin: origin, Client: server.Client()}
			if _, err := d.Refresh(context.Background(), "refresh"); !errors.Is(err, model.ErrControlUnavailable) {
				t.Fatal("JWKS failure became authorization denial", err)
			}
		})
	}
}

func (p *unavailableProvider) UserActive(context.Context, string) error { return p.err }
func (p *unavailableProvider) Refresh(context.Context, string) (Tokens, error) {
	p.refreshed = true
	return Tokens{}, nil
}

func TestRefreshDistinguishesOutageFromRevocation(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, model.ErrForbidden, context.Canceled} {
		p := &unavailableProvider{err: cause}
		db, err := store.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		c := model.Credential{UserID: "u", DeviceID: "d", ExpiresAt: time.Now().Add(time.Hour)}
		if err = db.Update(func(st *model.State) error {
			st.Users["u"] = model.User{ID: "u"}
			st.Devices["d"] = model.Device{ID: "d", UserID: "u"}
			st.Credentials[model.Hash("old")] = c
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		n := Native{Store: db, Provider: p}
		result, err := n.Refresh(context.Background(), c, "old", "refresh")
		if !errors.Is(err, cause) || p.refreshed || result.Token != "" {
			t.Fatal("failed check issued credentials", err)
		}
		if errors.Is(err, model.ErrControlUnavailable) != errors.Is(cause, context.DeadlineExceeded) {
			t.Fatal("wrong availability classification", err)
		}
	}
}

func TestDexOutageDoesNotBecomeInvalidCredentials(t *testing.T) {
	for _, status := range []int{429, 503, 401} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			d := &Dex{LocalURL: server.URL, Client: server.Client()}
			_, loginErr := d.Authorize(context.Background(), "u", "p", "n", "c")
			_, refreshErr := d.Refresh(context.Background(), "refresh")
			want := model.ErrControlUnavailable
			if status == 401 {
				want = model.ErrUnauthorized
			}
			if !errors.Is(loginErr, want) || !errors.Is(refreshErr, want) {
				t.Fatal(loginErr, refreshErr)
			}
		})
	}
}
