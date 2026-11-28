package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"netgo/internal/authorization"
	"netgo/internal/identity"
	"netgo/internal/model"
	"strconv"
	"strings"
	"time"
)

type ctxKey int

const credKey ctxKey = 1
const principalKey ctxKey = 2

type principal struct {
	device model.Device
}

func (s *Server) authorize(admin bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, u, d, e := s.Store.Identity(r.Context(), model.Hash(token))
		if errors.Is(e, model.ErrUnauthorized) && r.Method == http.MethodPost && r.URL.Path == "/netgo/v2/auth/refresh" {
			c, u, d, e = s.Store.RefreshIdentity(r.Context(), model.Hash(token))
		}
		if e != nil {
			failure(w, model.ControlFailure(e))
			return
		}
		if d.DeletePending || d.UserID != c.UserID || !time.Now().Before(c.ExpiresAt) || !u.Active(time.Now()) {
			failure(w, model.ErrUnauthorized)
			return
		}
		if admin && u.Role != "admin" {
			failure(w, model.ErrForbidden)
			return
		}
		stamp, nonce, sig := r.Header.Get("X-NetGo-Time"), r.Header.Get("X-NetGo-Nonce"), r.Header.Get("X-NetGo-Signature")
		ts, e := strconv.ParseInt(stamp, 10, 64)
		if e != nil || time.Since(time.Unix(ts, 0)) > 30*time.Second || time.Until(time.Unix(ts, 0)) > 30*time.Second || len(nonce) < 32 || len(nonce) > 128 {
			failure(w, model.ErrUnauthorized)
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if e != nil {
			failure(w, e)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		if !identity.Verify(d.PublicKey, identity.SignatureMessage(r.Method, r.URL.RequestURI(), stamp, nonce, b, token), sig) {
			failure(w, model.ErrUnauthorized)
			return
		}
		// Retain through the entire accepted timestamp window, including a stamp
		// 30 seconds in the future. SQLite arbitrates concurrent server processes.
		if e := s.Store.ConsumeNonce(r.Context(), model.Hash("api:"+c.DeviceID+":"+nonce), time.Now(), time.Now().Add(61*time.Second)); e != nil {
			failure(w, model.ControlFailure(e))
			return
		}
		ctx := context.WithValue(r.Context(), credKey, c)
		if admin {
			ctx = authorization.WithAdmin(ctx, c)
		}
		ctx = context.WithValue(ctx, principalKey, principal{d})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
