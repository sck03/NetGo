package client

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"netgo/internal/model"
	"time"
)

// Route selection is a single operation, never the lifetime of a connection.
// Parent cancellation and shorter recovery deadlines still take precedence.
const routeTimeout = 15 * time.Second

type routeSetter interface {
	Routes(context.Context, []model.Resource) error
}

func applySelectedRoutes(parent context.Context, engine routeSetter, resources []model.Resource, internet bool) error {
	ctx, cancel := context.WithTimeout(parent, routeTimeout)
	defer cancel()
	selected, err := SelectedResources(resources, internet)
	if err != nil {
		return err
	}
	if err = CheckConflicts(selected); err != nil {
		return err
	}
	return engine.Routes(ctx, selected)
}

func CheckConflicts(resources []model.Resource) error {
	addrs, e := net.InterfaceAddrs()
	if e != nil {
		return e
	}
	for _, a := range addrs {
		local, e := netip.ParsePrefix(a.String())
		if e != nil || local.Addr().IsLoopback() {
			continue
		}
		for _, r := range resources {
			if r.Protocol == "internet" && r.CIDR == "0.0.0.0/0" {
				continue
			}
			remote, e := netip.ParsePrefix(r.CIDR)
			if e != nil {
				return e
			}
			if local.Overlaps(remote) && remote.Bits() != 32 {
				return fmt.Errorf("route_conflict: %s overlaps local %s", remote, local)
			}
		}
	}
	return nil
}
