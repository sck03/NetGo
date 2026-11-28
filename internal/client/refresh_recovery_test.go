package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRefreshLostResponseDoesNotExposeOldCredential(t *testing.T) {
	for _, failures := range []int32{1, 2} {
		t.Run(string(rune('0'+failures)), func(t *testing.T) {
			var attempts, business atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/netgo/v2/auth/refresh" {
					if r.Header.Get("Authorization") != "Bearer old" {
						t.Error("recovery changed its original credential")
					}
					if attempts.Add(1) <= failures {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close() // The server committed, but its reply was lost.
						return
					}
					_, _ = w.Write([]byte(`{"token":"new"}`))
					return
				}
				business.Add(1)
				if r.Header.Get("Authorization") != "Bearer new" {
					t.Error("stale credential reached business API")
				}
			}))
			defer server.Close()
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			remote := &Remote{HTTP: server.Client(), Origin: server.URL, Token: "old", Private: key}
			_, err := remote.Refresh(context.Background(), "refresh")
			if failures == 1 && err != nil {
				t.Fatal(err)
			}
			if failures == 2 {
				if err == nil {
					t.Fatal("missing transport failure")
				}
				if err = remote.Call(context.Background(), "GET", "/netgo/v2/remote", nil, nil, ""); err == nil || err.Error() != "authentication_refresh_pending" {
					t.Fatal(err)
				}
				if business.Load() != 0 {
					t.Fatal("business request bypassed recovery fence")
				}
				if _, err = remote.Refresh(context.Background(), "refresh"); err != nil {
					t.Fatal(err)
				}
			}
			if err = remote.Call(context.Background(), "GET", "/netgo/v2/remote", nil, nil, ""); err != nil {
				t.Fatal(err)
			}
			if business.Load() != 1 {
				t.Fatal("recovered request not delivered")
			}
		})
	}
}

func TestRefreshRejectsRevocationWithoutRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	remote := &Remote{HTTP: server.Client(), Origin: server.URL, Token: "old", Private: key}
	if _, err := remote.Refresh(context.Background(), "refresh"); !terminalControlError(err) {
		t.Fatal(err)
	}
	if attempts.Load() != 1 {
		t.Fatal("revocation was retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := remote.Call(ctx, "GET", "/netgo/v2/remote", nil, nil, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
