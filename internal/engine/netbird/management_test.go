package netbird

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type failedTransport struct{}

func (failedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("response lost")
}

func TestRequestFailureCertainty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	management := Management{URL: server.URL, Client: &http.Client{Timeout: time.Second}}
	_, err := management.Request(context.Background(), "POST", "/users", map[string]string{"name": "New"}, nil)
	var failure *RequestFailure
	if !errors.As(err, &failure) || !failure.RequestMayHaveCommitted() {
		t.Fatal("lost response treated as unsent", err)
	}
	server.Close()
	_, err = management.Request(context.Background(), "POST", "/users", nil, nil)
	if !errors.As(err, &failure) || failure.RequestMayHaveCommitted() {
		t.Fatal("connection failure treated as committed", err)
	}
	management.Client.Transport = failedTransport{}
	_, err = management.Request(context.Background(), "POST", "/users", nil, nil)
	if !errors.As(err, &failure) || !failure.RequestMayHaveCommitted() {
		t.Fatal("custom transport certainty inferred without evidence", err)
	}
}
