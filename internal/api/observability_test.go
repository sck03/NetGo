package api

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"netgo/internal/diagnostics"
	"netgo/internal/site"
	"strings"
	"testing"
)

func TestRequestLogsExcludeCredentialsIdentifiersAndRequestBodies(t *testing.T) {
	var logs bytes.Buffer
	server := Server{Config: site.Config{Site: site.Profile{ID: "site"}}, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Monitor: diagnostics.NewMonitor()}
	r := httptest.NewRequest("POST", "/netgo/v2/auth/private-user?token=secret-query", strings.NewReader(`{"password":"secret-body"}`))
	r.Header.Set("Authorization", "Bearer secret-header")
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != 426 || w.Header().Get("X-NetGo-Trace") == "" {
		t.Fatal("missing failure trace")
	}
	for _, secret := range []string{"private-user", "secret-query", "secret-body", "secret-header"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log leaked %s", secret)
		}
	}
	if !strings.Contains(logs.String(), `"operation":"auth"`) || !strings.Contains(logs.String(), w.Header().Get("X-NetGo-Trace")) {
		t.Fatal("log lacks correlation")
	}
}
