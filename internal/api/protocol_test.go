package api

import (
	"net/http/httptest"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"strings"
	"testing"
)

func TestIncompatibleRequestsStopBeforeAnyAuthenticationOrMutation(t *testing.T) {
	server := &Server{Config: site.Config{Site: site.Profile{ID: "site", Origin: "https://site.test"}}}
	// Auth and Store intentionally remain nil: reaching either would panic.
	for _, version := range []string{"", "0", "1", "2, 2"} {
		for _, path := range []string{"/netgo/v2/auth/transactions", "/netgo/v2/admin/users", "/netgo/v2/sessions"} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{"Password":"must-not-be-processed"}`))
			if version != "" {
				r.Header.Set(protocol.Header, version)
			}
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, r)
			if w.Code != 426 || !strings.Contains(w.Body.String(), "protocol_incompatible") {
				t.Fatalf("version=%s path=%s status=%d", version, path, w.Code)
			}
		}
	}
}

func TestLegacyPathsCannotReachV2Handlers(t *testing.T) {
	server := &Server{Config: site.Config{Site: site.Profile{ID: "site", Origin: "https://site.test"}}}
	for _, path := range []string{"/netgo/v1/health", "/netgo/v1/auth/transactions", "/netgo/v1/remote/sessions"} {
		request := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		request.Header.Set(protocol.Header, "2")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code >= 200 && response.Code < 300 {
			t.Fatalf("legacy API accepted: %s %d", path, response.Code)
		}
	}
}
