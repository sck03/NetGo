package controlpath

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"netgo/internal/site"
	"strings"
	"testing"
	"time"
)

func testSite(t *testing.T, siteID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"protocol\":1,\"site_id\":\"" + siteID + "\"}"))
	}))
	t.Cleanup(srv.Close)
	return srv
}
func TestFallbackKeepsCanonicalTLSIdentity(t *testing.T) {
	srv := testSite(t, "site1")
	p := site.Profile{ID: "site1", Protocol: 1, Origin: srv.URL, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: "127.0.0.1:1"}, {ID: "vps", Kind: "auxiliary", Address: strings.TrimPrefix(srv.URL, "https://")}}}
	s, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	s.FallbackDelay = time.Millisecond
	c, e := s.Select(context.Background())
	if e != nil || c.ID != "vps" {
		t.Fatal(c, e)
	}
	res, e := s.Client().Get(p.Origin + "/netgo/v1/health")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	req, _ := http.NewRequest("GET", "https://another-site.test/netgo/v1/site", nil)
	if _, e = s.RoundTrip(req); e == nil {
		t.Fatal("cross-origin request accepted")
	}
}
func TestRejectWrongSiteAndUntrustedTLS(t *testing.T) {
	srv := testSite(t, "wrong")
	p := site.Profile{ID: "expected", Protocol: 1, Origin: srv.URL, Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: strings.TrimPrefix(srv.URL, "https://")}}}
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
