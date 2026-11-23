package api

import (
	"log/slog"
	"net/http"
	"netgo/internal/model"
	"strings"
	"time"
)

type observedResponse struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedResponse) WriteHeader(status int) {
	if w.written {
		return
	}
	w.status, w.written = status, true
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedResponse) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func requestClass(path string) string {
	for _, name := range []string{"health", "auth", "sessions", "admin", "site", "me", "peer"} {
		prefix := "/netgo/v2/" + name
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return name
		}
	}
	return "other"
}

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		trace := model.ID()[:16]
		w.Header().Set("X-NetGo-Trace", trace)
		out := &observedResponse{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(out, r)
		class := requestClass(r.URL.Path)
		if s.Monitor != nil {
			s.Monitor.Observe(out.status, class == "auth")
		}
		if s.Logger != nil && class != "health" {
			level := slog.LevelInfo
			if out.status >= 400 {
				level = slog.LevelWarn
			}
			// No query strings, resource IDs, headers, bodies, usernames or errors.
			s.Logger.Log(r.Context(), level, "api.request", "site_id", s.Config.Site.ID, "trace_id", trace, "operation", class, "status", out.status, "duration_ms", time.Since(started).Milliseconds())
		}
	})
}
