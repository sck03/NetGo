package main

import (
	"bytes"
	"context"
	"flag"
	"log/slog"
	"netgo/internal/ddns"
	"netgo/internal/process"
	"netgo/internal/site"
	"os"
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
	if site.Decode(bytes.NewReader(b), &c) != nil || c.Validate() != nil {
		slog.Error("invalid DDNS configuration")
		os.Exit(1)
	}
	token, e := site.Secret(c.TokenFile)
	if e != nil {
		os.Exit(1)
	}
	ctx, stop := process.StopContext()
	defer stop()
	events, err := ddns.NetworkEvents(ctx)
	if err != nil {
		slog.Warn("network event watcher unavailable; periodic DDNS remains active")
	}
	client := ddns.New(c, token)
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
		// Coalesce link/address/route bursts without flooding witnesses or the DNS API.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-events:
		}
	}
}
