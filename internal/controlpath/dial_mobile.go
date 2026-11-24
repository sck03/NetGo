//go:build android || ios

package controlpath

import (
	"context"
	nbnet "github.com/netbirdio/netbird/client/net"
	"net"
	"time"
)

func dialControl(ctx context.Context, network, address string, budget time.Duration) (net.Conn, error) {
	d := nbnet.NewDialer()
	d.Timeout = budget
	d.KeepAlive = 30 * time.Second
	return d.DialContext(ctx, network, address)
}
