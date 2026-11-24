package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"netgo/internal/gateway"
	"netgo/internal/ipc"
	"netgo/internal/privileged"
	"netgo/internal/site"
	"os"
	"time"
)

func main() {
	path := flag.String("config", "/etc/netgo/site.json", "configuration")
	flag.Parse()
	c, e := site.Load(*path)
	if e != nil {
		log.Fatal(e)
	}
	g := &gateway.NFT{Interface: c.Interface, LANInterface: c.LANInterface, Overlay: c.OverlayCIDR, LocalCIDRs: c.LocalCIDRs, Bypass: c.Bypass}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	e = g.Apply(ctx, nil)
	cancel()
	if e != nil {
		log.Fatal(e)
	}
	l, e := ipc.Listen(c.HelperSocket, "")
	if e != nil {
		log.Fatal(e)
	}
	if e = os.Chmod(c.HelperSocket, 0660); e != nil {
		log.Fatal(e)
	}
	defer l.Close()
	defer os.Remove(c.HelperSocket)
	srv := http.Server{Handler: privileged.Handler(g), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	log.Fatal(srv.Serve(l))
}
