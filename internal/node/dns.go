package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"netgo/internal/site"
	"sync"
	"time"
)

type dnsResult struct {
	Server   string
	FakeIP   bool
	Err      error
	Duration time.Duration
}
type dnsLookup func(context.Context, string) ([]netip.Addr, error)

func lookupDNS(ctx context.Context, server string) ([]netip.Addr, error) {
	resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(server, "53"))
	}}
	return resolver.LookupNetIP(ctx, "ip4", "example.com.")
}

// Check all configured resolvers within one deadline. Failure is never evidence
// of ordinary DNS; a Fake-IP fallback also needs its route and firewall policy.
func probeDNS(ctx context.Context, servers []string, lookup dnsLookup) ([]dnsResult, error) {
	if err := site.ValidateDNS(servers); err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, errors.New("请先配置 DNS 服务器")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	results := make([]dnsResult, len(servers))
	var workers sync.WaitGroup
	for i, server := range servers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			started := time.Now()
			addresses, err := lookup(ctx, server)
			result := dnsResult{Server: server, Err: err, Duration: time.Since(started)}
			valid := false
			for _, ip := range addresses {
				ip = ip.Unmap()
				if ip.Is4() && ip.IsGlobalUnicast() {
					valid = true
					result.FakeIP = result.FakeIP || netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
				}
			}
			if err == nil && !valid {
				result.Err = errors.New("没有可用的 IPv4 解析结果")
			}
			results[i] = result
		}()
	}
	workers.Wait()
	var failures []error
	for _, r := range results {
		if r.Err != nil {
			failures = append(failures, fmt.Errorf("DNS %s 查询失败：%w", r.Server, r.Err))
		}
	}
	return results, errors.Join(failures...)
}
