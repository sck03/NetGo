package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"netgo/internal/api"
	"netgo/internal/engine/netbird"
	"netgo/internal/identity"
	"netgo/internal/privileged"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"netgo/internal/webui"
	"os"
	"os/signal"
	"time"
)

func main() {
	if e := run(); e != nil {
		slog.Error("server stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "/etc/netgo/site.json", "configuration")
	flag.Parse()
	c, e := site.Load(*path)
	if e != nil {
		return e
	}
	s, e := store.Open(c.Database)
	if e != nil {
		return e
	}
	defer s.Close()
	token, e := site.Secret(c.ManagementTokenFile)
	if e != nil {
		return e
	}
	secret, e := site.Secret(c.NativeSecretFile)
	if e != nil {
		return e
	}
	cl := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}
	m := &netbird.Management{URL: c.ManagementURL, Token: token, Client: cl}
	reconciler := &netbird.Reconciler{Management: m, Store: s, SiteID: c.Site.ID, GatewayID: c.Site.GatewayPeerID}
	auth := &identity.Native{SiteID: c.Site.ID, Store: s, Provider: &identity.Dex{Origin: c.Site.Origin, LocalURL: c.ManagementURL, Secret: secret, Client: cl, CheckUser: m.UserActive}}
	leases := &session.Manager{Store: s, Gate: &privileged.Client{Path: c.HelperSocket}, Peers: m, TTL: time.Duration(c.LeaseSeconds) * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if e = leases.Reset(ctx); e != nil {
		return e
	}
	srv := &api.Server{Config: c, Store: s, Auth: auth, Sessions: leases, Management: m, Web: webui.Handler("admin"), Reconcile: reconciler.Apply}
	httpServer := &http.Server{Addr: c.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- httpServer.ListenAndServe() }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = leases.Reset(shutdown)
			return httpServer.Shutdown(shutdown)
		case e := <-errc:
			return e
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, 4*time.Second)
			if e = leases.Sweep(check); e != nil {
				slog.Error("lease sweep failed")
			}
			cancel()
		}
	}
}
