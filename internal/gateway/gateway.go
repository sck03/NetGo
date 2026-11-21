package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"netgo/internal/model"
	"netgo/internal/site"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type Gate interface {
	Apply(context.Context, []model.Lease) error
}
type NFT struct {
	mu                               sync.Mutex
	Interface, LANInterface, Overlay string
	Run                              func(context.Context, string) error
}

func (n *NFT) Apply(ctx context.Context, leases []model.Lease) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	script, e := Render(n.Interface, n.LANInterface, n.Overlay, leases, time.Now())
	if e != nil {
		return e
	}
	if n.Run != nil {
		return n.Run(ctx, script)
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("nft apply failed: %w: %s", e, stderr.String())
	}
	return nil
}

// Render replaces only the owned table, atomically. Timeouts remain effective if
// both the application and helper crash. The early drop also covers established flows.
func Render(iface, lan, overlay string, leases []model.Lease, now time.Time) (string, error) {
	if !site.ValidID(iface) || !site.ValidID(lan) || iface == lan {
		return "", errors.New("invalid interface")
	}
	pool, e := netip.ParsePrefix(overlay)
	if e != nil || !pool.Addr().Is4() {
		return "", errors.New("invalid overlay")
	}
	var b strings.Builder
	fmt.Fprintln(&b, "add table inet netgo\nflush table inet netgo")
	fmt.Fprintln(&b, "add set inet netgo active4 { type ipv4_addr; flags timeout; }")
	fmt.Fprintln(&b, "add chain inet netgo input { type filter hook input priority -150; policy accept; }")
	fmt.Fprintf(&b, "add rule inet netgo input iifname %q counter drop\n", iface)
	fmt.Fprintln(&b, "add chain inet netgo forward { type filter hook forward priority -150; policy accept; }")
	fmt.Fprintln(&b, "add chain inet netgo resources")
	fmt.Fprintf(&b, "add rule inet netgo forward iifname %q oifname %q ip saddr @active4 jump resources\n", iface, lan)
	fmt.Fprintf(&b, "add rule inet netgo forward iifname %q counter drop\n", iface)
	fmt.Fprintf(&b, "add rule inet netgo forward oifname %q ip daddr @active4 ct state established,related accept\n", iface)
	fmt.Fprintf(&b, "add rule inet netgo forward oifname %q counter drop\n", iface)
	seen := map[string]bool{}
	sort.Slice(leases, func(i, j int) bool { return leases[i].ID < leases[j].ID })
	for _, l := range leases {
		ttl := int(l.ExpiresAt.Sub(now).Seconds())
		if ttl <= 0 {
			continue
		}
		if ttl > 120 {
			return "", errors.New("lease exceeds 120 seconds")
		}
		ip, e := netip.ParseAddr(l.IP)
		if e != nil || !pool.Contains(ip) || seen[l.IP] {
			return "", errors.New("invalid/duplicate lease IP")
		}
		seen[l.IP] = true
		fmt.Fprintf(&b, "add element inet netgo active4 { %s timeout %ds }\n", ip, ttl)
		for _, r := range l.Resources {
			if !r.Enabled {
				continue
			}
			if e := site.ValidateResource(r, overlay); e != nil {
				return "", e
			}
			fmt.Fprintf(&b, "add rule inet netgo resources ip saddr %s ip daddr %s ", ip, r.CIDR)
			if r.Protocol == "icmp" {
				fmt.Fprint(&b, "ip protocol icmp ")
			} else {
				ports := make([]string, len(r.Ports))
				for i, p := range r.Ports {
					ports[i] = fmt.Sprint(p)
				}
				fmt.Fprintf(&b, "%s dport { %s } ", r.Protocol, strings.Join(ports, ", "))
			}
			fmt.Fprintln(&b, "counter accept")
		}
	}
	return b.String(), nil
}
