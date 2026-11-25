package controlpath

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testSite(t *testing.T, siteID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.Health{Protocol: 2, SiteID: siteID, Capabilities: protocol.Capabilities()})
	}))
	t.Cleanup(srv.Close)
	return srv
}
func TestFallbackKeepsCanonicalTLSIdentity(t *testing.T) {
	srv := testSite(t, "site1")
	p := site.Profile{ID: "site1", Protocol: 2, Origin: srv.URL, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: "127.0.0.1:1"}, {ID: "vps", Kind: "auxiliary", Address: strings.TrimPrefix(srv.URL, "https://")}}}
	s, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	s.FallbackDelay = time.Millisecond
	c, e := s.Select(context.Background())
	if e != nil || c.ID != "vps" {
		t.Fatal(c, e)
	}
	res, e := s.Client().Get(p.Origin + "/netgo/v2/health")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	req, _ := http.NewRequest("GET", "https://another-site.test/netgo/v2/site", nil)
	if _, e = s.RoundTrip(req); e == nil {
		t.Fatal("cross-origin request accepted")
	}
}
func TestRejectWrongSiteAndUntrustedTLS(t *testing.T) {
	srv := testSite(t, "wrong")
	p := site.Profile{ID: "expected", Protocol: 2, Origin: srv.URL, Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: strings.TrimPrefix(srv.URL, "https://")}}}
	s, _ := New(p)
	if _, e := s.Select(context.Background()); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
	s.Roots = x509.NewCertPool()
	s.Roots.AddCert(srv.Certificate())
	if _, e := s.Select(context.Background()); e == nil {
		t.Fatal("wrong site accepted")
	}
}

func TestMultiWANRecoveryChecksHealthySecondEntry(t *testing.T) {
	srv := testSite(t, "site1")
	address := strings.TrimPrefix(srv.URL, "https://")
	profile := site.Profile{ID: "site1", Protocol: 2, Origin: srv.URL,
		CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})),
		Candidates: []site.Candidate{{ID: "wan1", Kind: "direct", Address: "127.0.0.1:1"},
			{ID: "wan2", Kind: "direct", Address: address}, {ID: "wan3", Kind: "direct", Address: "127.0.0.1:1"},
			{ID: "vps", Kind: "auxiliary", Address: address}}}
	s, err := New(profile)
	if err != nil {
		t.Fatal(err)
	}
	s.selected = profile.Candidates[3]
	s.lastSwitch = time.Now().Add(-time.Minute)
	for i := 0; i < 3; i++ {
		if err := s.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if i < 2 && s.Current().ID != "vps" {
			t.Fatal("switched without stable WAN confirmation")
		}
	}
	if s.Current().ID != "wan2" {
		t.Fatal("failed first WAN prevented return to a healthy WAN", s.Current())
	}
}

func TestSlowAPIHandshakeDoesNotInheritProbeDeadline(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		time.Sleep(120 * time.Millisecond)
		return nil, nil
	}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	candidate := site.Candidate{ID: "wan", Kind: "direct", Address: strings.TrimPrefix(srv.URL, "https://")}
	profile := site.Profile{ID: "slow", Protocol: 2, Origin: srv.URL,
		CA:         string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})),
		Candidates: []site.Candidate{candidate}}
	selector, err := New(profile)
	if err != nil {
		t.Fatal(err)
	}
	selector.ProbeTimeout = 40 * time.Millisecond
	selector.selected = candidate
	if err := selector.probe(context.Background(), candidate); err == nil {
		t.Fatal("health probe exceeded its separate budget")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/netgo/v2/sessions", strings.NewReader("{}"))
	resp, err := selector.Client().Do(req)
	if err != nil {
		t.Fatal("API handshake was cut off by probe timeout:", err)
	}
	resp.Body.Close()
	if requests.Load() != 1 {
		t.Fatal("API write replayed", requests.Load())
	}
	// Reuse is intentional now; force a new connection to exercise handshake cancellation.
	selector.CloseIdleConnections()
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	req, _ = http.NewRequestWithContext(short, http.MethodPost, srv.URL+"/netgo/v2/sessions", strings.NewReader("{}"))
	if resp, err := selector.Client().Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("caller cancellation was ignored")
	}
	if requests.Load() != 1 {
		t.Fatal("cancelled request reached handler")
	}
}

func TestControlPoolReusesAndReleasesTLSConnections(t *testing.T) {
	var handshakes atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	srv.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { handshakes.Add(1); return nil, nil }}
	srv.StartTLS()
	defer srv.Close()
	candidate := site.Candidate{ID: "wan", Kind: "direct", Address: strings.TrimPrefix(srv.URL, "https://")}
	selector, e := New(site.Profile{ID: "pool", Protocol: 2, Origin: srv.URL, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), Candidates: []site.Candidate{candidate}})
	if e != nil {
		t.Fatal(e)
	}
	defer selector.CloseIdleConnections()
	selector.selected = candidate
	for i := 0; i < 10; i++ {
		resp, e := selector.Client().Get(srv.URL + "/netgo/v2/me")
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
	}
	if handshakes.Load() != 1 {
		t.Fatal("requests did not reuse TLS", handshakes.Load())
	}
	selector.CloseIdleConnections()
	resp, e := selector.Client().Get(srv.URL + "/netgo/v2/me")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if handshakes.Load() != 2 {
		t.Fatal("connection pool not closed")
	}
}
