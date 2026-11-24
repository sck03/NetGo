//go:build !linux && !ios

package controlpath

import (
	"context"
	"net"
	"time"
)

func dialControl(ctx context.Context, network, address string, budget time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: budget, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
}
