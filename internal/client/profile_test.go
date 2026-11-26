package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"netgo/internal/controlpath"
	"netgo/internal/identity"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthenticatedProfileSyncProbesBeforePersisting(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	var published atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/netgo/v2/health" {
			_ = json.NewEncoder(w).Encode(protocol.Health{Protocol: 2, SiteID: "site", Capabilities: protocol.Capabilities(), ServerUnix: time.Now().Unix()})
			return
		}
		if r.URL.Path != "/netgo/v2/site" || r.Header.Get("Authorization") != "Bearer token" || r.Header.Get(protocol.Header) != "2" {
			t.Error("unauthenticated profile retrieval")
			http.Error(w, "unauthorized", 401)
			return
		}
		message := identity.SignatureMessage("GET", r.URL.RequestURI(), r.Header.Get("X-NetGo-Time"), r.Header.Get("X-NetGo-Nonce"), nil, "token")
		if !identity.Verify(base64.RawURLEncoding.EncodeToString(public), message, r.Header.Get("X-NetGo-Signature")) {
			t.Error("profile retrieval signature invalid")
			return
		}
		_ = json.NewEncoder(w).Encode(published.Load())
	}))
	defer server.Close()
	old := site.Profile{ID: "site", Name: "My name", Protocol: 2, ConfigRevision: 2, Origin: server.URL, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), Candidates: []site.Candidate{{ID: "old", Kind: "direct", Address: strings.TrimPrefix(server.URL, "https://")}}}
	for _, scenario := range []string{"success", "stale", "bad-entry", "trust-change"} {
		t.Run(scenario, func(t *testing.T) {
			controller := &Controller{Root: t.TempDir()}
			data, _ := json.Marshal(old)
			file := filepath.Join(controller.Root, "site.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			next := old
			next.ConfigRevision = 3
			next.Name = "Server name"
			next.Candidates = append([]site.Candidate{}, old.Candidates...)
			next.Candidates[0].ID = "updated"
			switch scenario {
			case "stale":
				next.ConfigRevision = 1
			case "bad-entry":
				next.Candidates[0].Address = "127.0.0.1:1"
			case "trust-change":
				next.Origin = "https://untrusted.example"
			}
			published.Store(next)
			selector, err := controlpath.New(old)
			if err != nil {
				t.Fatal(err)
			}
			remote := &Remote{HTTP: selector.Client(), Origin: old.Origin, Token: "token", Private: private}
			result, _, err := controller.synchronizeProfile(context.Background(), remote, old, selector)
			saved, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario == "success" {
				if err != nil || result.ConfigRevision != 3 || result.Name != "My name" || !strings.Contains(string(saved), "updated") {
					t.Fatal("profile did not update", err)
				}
				if controller.Status().ProfilesRevision != 1 {
					t.Fatal("missing profile change notification")
				}
			} else if err == nil || string(saved) != string(data) {
				t.Fatalf("invalid update changed protected profile: %v", err)
			}
		})
	}
}

func TestExplicitImportTrustAndRevision(t *testing.T) {
	old := site.Profile{ID: "site", Name: "Site", Protocol: 2, ConfigRevision: 2, Origin: "https://old.example:10443", Candidates: []site.Candidate{{ID: "wan", Kind: "direct", Address: "old.example:10443"}}}
	for _, tc := range []struct {
		name     string
		trusted  bool
		revision uint64
		origin   string
		accept   bool
	}{
		{"automatic host change", false, 3, "https://new.example:10443", false},
		{"confirmed host change", true, 3, "https://new.example:10443", true},
		{"confirmed stale change", true, 1, "https://new.example:10443", false},
		{"confirmed same revision", true, 2, "https://new.example:10443", false},
		{"versioned port change", false, 3, "https://old.example:11443", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{Root: t.TempDir()}
			if err := c.Import(old); err != nil {
				t.Fatal(err)
			}
			next := old
			next.ConfigRevision = tc.revision
			next.Origin = tc.origin
			var err error
			if tc.trusted {
				err = c.ImportTrusted(next)
			} else {
				err = c.Import(next)
			}
			if (err == nil) != tc.accept {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.accept, err)
			}
			profiles, e := c.Profiles()
			if e != nil || len(profiles) != 1 {
				t.Fatal(profiles, e)
			}
			want := old.Origin
			if tc.accept {
				want = next.Origin
			}
			if profiles[0].Origin != want {
				t.Fatal("failed import replaced the saved profile")
			}
			wantRevision := uint64(1)
			if tc.accept {
				wantRevision++
			}
			if c.Status().ProfilesRevision != wantRevision {
				t.Fatal("profile notification changed on rejected import")
			}
		})
	}
}
