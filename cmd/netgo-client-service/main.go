//go:build !windows

// Linux diagnostic client. The Windows product is native C++.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"netgo/internal/client"
	"netgo/internal/engine/netbird"
	"netgo/internal/ipc"
	"netgo/internal/process"
	"netgo/internal/vault"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

func main() {
	u, e := user.Current()
	if e != nil {
		log.Fatal(e)
	}
	root, _ := os.UserConfigDir()
	root = filepath.Join(root, "NetGo")
	socket := filepath.Join(root, "client.sock")
	daemon := "/var/run/netbird.sock"
	socketFlag := flag.String("socket", socket, "user IPC endpoint")
	daemonFlag := flag.String("daemon", daemon, "NetBird IPC endpoint")
	rootFlag := flag.String("root", root, "profile directory")
	flag.Parse()
	d, e := netbird.OpenDaemon(*daemonFlag)
	if e != nil {
		log.Fatal(e)
	}
	defer d.Close()
	c := &client.Controller{Root: *rootFlag, Vault: vault.Vault{Root: filepath.Join(*rootFlag, "vault")}, Engine: d}
	l, e := ipc.Listen(*socketFlag, u.Uid)
	if e != nil {
		log.Fatal(e)
	}
	defer l.Close()
	defer os.Remove(*socketFlag)
	s := http.Server{Handler: c.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
	ctx, stop := process.StopContext()
	defer stop()
	go func() {
		<-ctx.Done()
		if err := c.Disconnect(); err != nil {
			log.Print("client network cleanup failed")
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdown)
	}()
	if err := s.Serve(l); err != nil && err != http.ErrServerClosed {
		log.Print(err)
	}
}
