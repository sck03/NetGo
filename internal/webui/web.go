package webui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var assets embed.FS

func Handler(mode string) http.Handler {
	root, _ := fs.Sub(assets, "dist")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.URL.Path == "/netgo-ui/mode" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"mode": mode})
			return
		}
		files.ServeHTTP(w, r)
	})
}
