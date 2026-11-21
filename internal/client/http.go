package client

import (
	"encoding/json"
	"net/http"
	"netgo/internal/site"
)

func (c *Controller) Handler() http.Handler {
	m := http.NewServeMux()
	write := func(w http.ResponseWriter, v any, e error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if e != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	m.HandleFunc("GET /desktop/status", func(w http.ResponseWriter, r *http.Request) { write(w, c.Status(), nil) })
	m.HandleFunc("GET /desktop/profiles", func(w http.ResponseWriter, r *http.Request) { v, e := c.Profiles(); write(w, v, e) })
	m.HandleFunc("POST /desktop/import", func(w http.ResponseWriter, r *http.Request) {
		var p site.Profile
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if e := site.Decode(r.Body, &p); e != nil {
			write(w, nil, e)
			return
		}
		write(w, map[string]bool{"imported": true}, c.Import(p))
	})
	m.HandleFunc("POST /desktop/connect", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Site, Alias, Password string
			Replace               bool
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if e := site.Decode(r.Body, &b); e != nil {
			write(w, nil, e)
			return
		}
		e := c.Connect(b.Site, b.Alias, b.Password, b.Replace)
		b.Password = ""
		write(w, c.Status(), e)
	})
	m.HandleFunc("POST /desktop/disconnect", func(w http.ResponseWriter, r *http.Request) { e := c.Disconnect(); write(w, c.Status(), e) })
	return m
}
