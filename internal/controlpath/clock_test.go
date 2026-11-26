package controlpath

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"strings"
	"testing"
	"time"
)

func TestTrustedServerClockIsIndependentOfClientWallClock(t *testing.T) {
	for _, skew := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		t.Run(skew.String(), func(t *testing.T) {
			remote := time.Now().Add(skew).Truncate(time.Second)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(protocol.Health{Protocol: 2, SiteID: "clock", Capabilities: protocol.Capabilities(), ServerUnix: remote.Unix()})
			}))
			defer server.Close()
			profile := site.Profile{Name: "Test site", GatewayIP: "100.96.0.1", ID: "clock", Protocol: 2, Origin: server.URL, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: strings.TrimPrefix(server.URL, "https://")}}}
			selector, err := New(profile)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = selector.Select(context.Background()); err != nil {
				t.Fatal(err)
			}
			delta := selector.Now().Sub(remote)
			if delta < 0 || delta > time.Second {
				t.Fatal("did not adopt trusted server time", delta)
			}
			selector.mu.Lock()
			sample := selector.clocks["local"]
			sample.received = sample.received.Add(-2 * time.Minute)
			selector.clocks["local"] = sample
			selector.mu.Unlock()
			if selector.Now().Before(remote.Add(2 * time.Minute)) {
				t.Fatal("elapsed time did not expire server deadlines")
			}
		})
	}
}
