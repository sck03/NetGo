package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallCancelsWhileRefreshWaitsOnNetwork(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/netgo/v2/auth/refresh" {
			t.Error("cancelled call reached server")
			return
		}
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"token":"new"}`))
	}))
	defer server.Close()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	remote := &Remote{HTTP: server.Client(), Origin: server.URL, Token: "old", Private: key}
	done := make(chan error, 1)
	go func() { _, err := remote.Refresh(context.Background(), "refresh"); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	call := make(chan error, 1)
	go func() { call <- remote.Call(ctx, "GET", "/netgo/v2/remote", nil, nil, "") }()
	select {
	case err := <-call:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request ignored cancellation while waiting for credentials")
	}
	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAuthGateConcurrentReadersAndCancelledWriter(t *testing.T) {
	var gate authGate
	first, _ := gate.acquire(context.Background(), false)
	second, _ := gate.acquire(context.Background(), false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.acquire(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	second()
	first()
	release, err := gate.acquire(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
