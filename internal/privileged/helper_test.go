package privileged

import (
	"context"
	"net/http"
	"net/http/httptest"
	"netgo/internal/model"
	"strings"
	"testing"
)

type helperGate struct {
	applied, renewed int
	installed        bool
}

func (g *helperGate) Apply(context.Context, []model.Lease) error { g.applied++; return nil }
func (g *helperGate) Renew(context.Context, model.Lease) (bool, error) {
	g.renewed++
	return g.installed, nil
}
func TestHelperRenewContract(t *testing.T) {
	g := &helperGate{}
	handler := Handler(g)
	for _, tc := range []struct {
		path, body string
		installed  bool
		code       int
	}{
		{"/apply", `[]`, false, http.StatusOK},
		{"/renew", `[{"id":"lease"}]`, false, http.StatusConflict},
		{"/renew", `[{"id":"lease"}]`, true, http.StatusOK},
		{"/renew", `[]`, true, http.StatusBadRequest},
		{"/renew", `[{"id":"a"},{"id":"b"}]`, true, http.StatusBadRequest},
	} {
		g.installed = tc.installed
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if response.Code != tc.code {
			t.Fatalf("%s %s: status %d, want %d", tc.path, tc.body, response.Code, tc.code)
		}
	}
	if g.applied != 1 || g.renewed != 2 {
		t.Fatal("helper dispatched invalid membership", g.applied, g.renewed)
	}
}
