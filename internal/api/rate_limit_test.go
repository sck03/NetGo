package api

import (
	"net/http/httptest"
	"netgo/internal/protocol"
	"netgo/internal/store"
	"testing"
	"time"
)

func TestAnonymousTrafficCannotConsumeRefreshBudget(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{Store: db}
	handler := s.Handler()
	for i := 0; i < 400; i++ {
		r := httptest.NewRequest("GET", "/netgo/v2/auth/not-a-route", nil)
		r.Header.Set(protocol.Header, "2")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	for i := 0; i < 1000; i++ {
		s.publicRates.allow("global", 900, time.Now())
	}
	r := httptest.NewRequest("POST", "/netgo/v2/auth/refresh", nil)
	r.Header.Set(protocol.Header, "2")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("refresh reached anonymous limiter", w.Code)
	}
	for i := 0; i < 12; i++ {
		if !s.refreshRates.allow("device", 12, time.Now()) {
			t.Fatal("refresh budget shared")
		}
	}
	if s.refreshRates.allow("device", 12, time.Now()) || !s.refreshRates.allow("another", 12, time.Now()) {
		t.Fatal("device budgets not independent")
	}
}
