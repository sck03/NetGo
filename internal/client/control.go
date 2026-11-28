package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/protocol"
	"strconv"
	"time"
)

type Remote struct {
	auth           authGate
	refreshPending bool
	HTTP           *http.Client
	Origin, Token  string
	Private        ed25519.PrivateKey
	Now            func() time.Time
}

func terminalControlError(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case "unauthorized", "forbidden", "not_found", "lease_expired", "session_conflict", "internet_forbidden":
		return true
	}
	return false
}

func (r *Remote) Call(ctx context.Context, method, path string, in, out any, key string) error {
	release, err := r.auth.acquire(ctx, false)
	if err != nil {
		return err
	}
	defer release()
	if r.refreshPending {
		return errors.New("authentication_refresh_pending")
	}
	return r.call(ctx, method, path, in, out, key)
}

// Rotate credentials between requests. In-flight desktop requests finish with
// their original proof before the server invalidates that credential.
func (r *Remote) Refresh(ctx context.Context, refreshToken string) (identity.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var result identity.Result
	release, err := r.auth.acquire(ctx, true)
	if err != nil {
		return result, err
	}
	defer release()
	// A failed response may follow a committed rotation. Keep business requests
	// fenced until the old credential has recovered that exact result. Splitting
	// the four-second budget leaves time for one immediate recovery attempt.
	r.refreshPending = true
	for attempt := 0; attempt < 2; attempt++ {
		result = identity.Result{}
		request, stop := context.WithTimeout(ctx, 2*time.Second)
		err = r.call(request, "POST", "/netgo/v2/auth/refresh", map[string]string{"refresh_token": refreshToken}, &result, "")
		stop()
		if err == nil {
			if result.Token == "" {
				err = errors.New("invalid_auth_response")
			} else {
				r.Token = result.Token
				r.refreshPending = false
				return result, nil
			}
		}
		if terminalControlError(err) || ctx.Err() != nil {
			break
		}
	}
	return result, err
}

func (r *Remote) call(ctx context.Context, method, path string, in, out any, key string) error {
	var b []byte
	if in != nil {
		var err error
		b, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, r.Origin+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(protocol.Header, strconv.Itoa(model.Protocol))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if r.Token != "" {
		now := time.Now()
		if r.Now != nil {
			now = r.Now()
		}
		stamp := strconv.FormatInt(now.Unix(), 10)
		nonce := model.ID()
		message := identity.SignatureMessage(method, path, stamp, nonce, b, r.Token)
		req.Header.Set("Authorization", "Bearer "+r.Token)
		req.Header.Set("X-NetGo-Time", stamp)
		req.Header.Set("X-NetGo-Nonce", nonce)
		req.Header.Set("X-NetGo-Signature", base64.RawURLEncoding.EncodeToString(ed25519.Sign(r.Private, []byte(message))))
	}
	res, e := r.HTTP.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("control_unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&v)
		if v.Error == "" {
			v.Error = fmt.Sprintf("HTTP_%d", res.StatusCode)
		}
		return errors.New(v.Error)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
	}
	return nil
}
