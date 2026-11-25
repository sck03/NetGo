package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"netgo/internal/api"
	"netgo/internal/devices"
	"netgo/internal/diagnostics"
	"netgo/internal/engine/netbird"
	"netgo/internal/identity"
	"netgo/internal/privileged"
	"netgo/internal/process"
	"netgo/internal/remote"
	"netgo/internal/session"
	"netgo/internal/site"
	"netgo/internal/store"
	"netgo/internal/webui"
	"os"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
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
	reconciler := &netbird.Reconciler{Management: m, Store: s, SiteID: c.Site.ID, GatewayID: c.Site.GatewayPeerID, LANInterface: c.LANInterface, Bypass: c.Bypass}
	auth := &identity.Native{SiteID: c.Site.ID, Store: s, Provider: &identity.Dex{Origin: c.Site.Origin, LocalURL: c.ManagementURL, Secret: secret, Client: cl, CheckUser: m.UserActive}}
	leases := &session.Manager{Store: s, Gate: &privileged.Client{Path: c.HelperSocket}, Peers: m, TTL: time.Duration(c.LeaseSeconds) * time.Second}
	ctx, stop := process.StopContext()
	defer stop()
	if e = leases.Reset(ctx); e != nil {
		return e
	}
	monitor := diagnostics.NewMonitor()
	go monitor.Run(ctx, c)
	networkDone := make(chan struct{})
	go func() { defer close(networkDone); reconciler.Run(ctx) }()
	defer func() { stop(); <-networkDone }()
	remoteKey, e := remote.LoadKey(c.Database + ".remote-key")
	if e != nil {
		return e
	}
	remotes := &remote.Manager{Store: s, SiteID: c.Site.ID, Key: remoteKey, Peers: m}
	deviceService := &devices.Service{Store: s, Sessions: leases, Management: m}
	srv := &api.Server{Config: c, Store: s, Auth: auth, Sessions: leases, Remote: remotes, Devices: deviceService, Management: m, Web: webui.Handler("admin"), Reconcile: reconciler.Ensure, Monitor: monitor, Logger: slog.Default()}
	httpServer := &http.Server{Addr: c.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- httpServer.ListenAndServe() }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var nextCleanup time.Time
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
			if e = remotes.Sweep(); e != nil {
				slog.Error("remote sweep failed")
			}
			check, cancel := context.WithTimeout(ctx, 4*time.Second)
			if e = leases.Sweep(check); e != nil {
				slog.Error("lease sweep failed")
			}
			cancel()
			if !time.Now().Before(nextCleanup) {
				cleanup, cancel := context.WithTimeout(ctx, 4*time.Second)
				if err := deviceService.Retry(cleanup); err != nil {
					slog.Warn("device cleanup pending")
				}
				cancel()
				nextCleanup = time.Now().Add(time.Minute)
			}
		}
	}
}
