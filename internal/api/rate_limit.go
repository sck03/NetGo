package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type rateWindow struct {
	start time.Time
	count int
}
type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateWindow
}

func (l *rateLimiter) allow(key string, limit int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = make(map[string]rateWindow)
	}
	w, ok := l.entries[key]
	if !ok && len(l.entries) >= 4096 {
		for k, v := range l.entries {
			if now.Sub(v.start) >= time.Minute {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= 4096 {
			return false
		}
	}
	if now.Sub(w.start) >= time.Minute {
		w = rateWindow{start: now}
	}
	if w.count >= limit {
		return false
	}
	w.count++
	l.entries[key] = w
	return true
}

func rateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	reply(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
}

// Apply only to registered anonymous transaction routes. Authenticated refresh
// has a separate per-device budget after signature verification, and account
// password attempts are independently bounded by identity.Native.
func (s *Server) anonymous(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		now := time.Now()
		// Loopback is commonly the local TLS proxy; do not collapse all its
		// clients into one small peer budget or trust arbitrary forwarded headers.
		ip := net.ParseIP(peer)
		if (ip == nil || !ip.IsLoopback()) && !s.publicRates.allow("peer:"+peer, 90, now) || !s.publicRates.allow("global", 900, now) {
			rateLimited(w)
			return
		}
		next(w, r)
	}
}
