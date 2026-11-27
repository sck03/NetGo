package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"netgo/internal/model"

	"github.com/coreos/go-oidc/v3/oidc"
)

type verificationFailureKey struct{}
type verificationFailure struct{ cause error }

// The verifier formats signature errors as text. Capture the original typed
// cause on this verification only, while retaining RemoteKeySet's shared cache.
type availableKeySet struct{ oidc.KeySet }

func (k availableKeySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	payload, err := k.KeySet.VerifySignature(ctx, jwt)
	if state, ok := ctx.Value(verificationFailureKey{}).(*verificationFailure); ok {
		state.cause = err
	}
	return payload, err
}

type keyTransport struct{ http.RoundTripper }

func (t keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(r)
	if err != nil {
		return nil, model.ControlFailure(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, model.ErrControlUnavailable
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || !json.Valid(data) {
		return nil, model.ErrControlUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func (d *Dex) keys() oidc.KeySet {
	client := *d.Client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = keyTransport{transport}
	return availableKeySet{oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), &client), d.LocalURL+"/oauth2/keys")}
}
