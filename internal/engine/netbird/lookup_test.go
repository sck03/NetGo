package netbird

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"netgo/internal/model"
	"testing"
	"time"
)

func TestLookupUsesFreshStateAndSkipsDisappearedPeers(t *testing.T) {
	blocked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users":
			if blocked {
				w.Write([]byte(`[{"id":"u","is_blocked":true,"status":"active"}]`))
			} else {
				w.Write([]byte(`[{"id":"u","is_blocked":false,"status":"active"}]`))
			}
		case "/api/peers":
			w.Write([]byte(`[{"id":"gone","user_id":"u"},{"id":"right","user_id":"u"}]`))
		case "/api/peers/gone":
			w.WriteHeader(404)
		case "/api/peers/right":
			w.Header().Set("X-NetGo-Peer-Key", "key")
			w.Write([]byte(`{"id":"right","user_id":"u"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 1
	defer transport.CloseIdleConnections()
	m := Management{URL: server.URL, Client: &http.Client{Transport: transport, Timeout: time.Second}}
	if err := m.UserActive(context.Background(), "u"); err != nil {
		t.Fatal(err)
	}
	blocked = true
	if err := m.UserActive(context.Background(), "u"); !errors.Is(err, model.ErrForbidden) {
		t.Fatal("revocation hidden", err)
	}
	if err := m.UserActive(context.Background(), "missing"); !errors.Is(err, model.ErrForbidden) {
		t.Fatal(err)
	}
	p, err := m.FindPeer(context.Background(), "u", "key")
	if err != nil || p.ID != "right" {
		t.Fatal("stale peer or bounded pool prevented lookup", p, err)
	}
}
