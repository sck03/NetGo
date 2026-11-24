package node

import (
	"context"
	"net"
	"net/netip"
	"time"
)

func detectFakeIP(dns string) bool {
	resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(dns, "53"))
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addresses, err := resolver.LookupIP(ctx, "ip4", "example.com")
	if err != nil {
		return false
	}
	rangeIP := netip.MustParsePrefix("198.18.0.0/15")
	for _, ip := range addresses {
		value, ok := netip.AddrFromSlice(ip)
		if ok && rangeIP.Contains(value.Unmap()) {
			return true
		}
	}
	return false
}
