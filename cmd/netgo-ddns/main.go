package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"netgo/internal/ddns"
	"netgo/internal/site"
	"os"
	"os/signal"
	"time"
)

func main() {
	path := flag.String("config", "/etc/netgo/ddns.json", "DDNS config")
	flag.Parse()
	b, e := os.ReadFile(*path)
	if e != nil {
		slog.Error("DDNS config unavailable")
		os.Exit(1)
	}
	var c ddns.Config
	if json.Unmarshal(b, &c) != nil || c.IntervalSeconds < 30 {
		os.Exit(1)
	}
	token, e := site.Secret(c.TokenFile)
	if e != nil {
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := ddns.Cloudflare{Config: c, Token: token, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	t := time.NewTicker(time.Duration(c.IntervalSeconds) * time.Second)
	defer t.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 25*time.Second)
		changed, e := client.Update(check)
		cancel()
		if e != nil {
			slog.Warn("DDNS update unavailable", "reason", e)
		} else if changed {
			slog.Info("DDNS address updated")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
