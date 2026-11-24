package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"netgo/internal/site"
	"time"
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
			Internet              bool
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if e := site.Decode(r.Body, &b); e != nil {
			write(w, nil, e)
			return
		}
		e := c.ConnectMode(b.Site, b.Alias, b.Password, b.Replace, b.Internet)
		b.Password = ""
		write(w, c.Status(), e)
	})
	m.HandleFunc("POST /desktop/disconnect", func(w http.ResponseWriter, r *http.Request) { e := c.Disconnect(); write(w, c.Status(), e) })
	m.HandleFunc("GET /desktop/remote/context", func(w http.ResponseWriter, r *http.Request) {
		v, e := c.RemoteContext(r.URL.Query().Get("site"))
		write(w, v, e)
	})
	m.HandleFunc("POST /desktop/remote/call", func(w http.ResponseWriter, r *http.Request) {
		var request RemoteRequest
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		if e := site.Decode(r.Body, &request); e != nil {
			write(w, nil, e)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		v, e := c.RemoteCommand(ctx, request)
		write(w, v, e)
	})
	m.HandleFunc("POST /desktop/remote/sign", func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Site, Message string }
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if e := site.Decode(r.Body, &request); e != nil {
			write(w, nil, e)
			return
		}
		message, e := base64.RawURLEncoding.Strict().DecodeString(request.Message)
		if e != nil {
			write(w, nil, e)
			return
		}
		v, e := c.SignPossession(request.Site, string(message))
		write(w, map[string]string{"signature": v}, e)
	})
	return m
}
