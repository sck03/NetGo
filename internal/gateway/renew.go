package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"netgo/internal/model"
	"strings"
	"time"
)

// Renew replaces only this membership's timeout in one atomic nft batch. It
// never creates a new authorization or changes an installed resource policy.
func (n *NFT) Renew(ctx context.Context, lease model.Lease) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	if len(n.pending) != 0 || n.lastFull.IsZero() || now.Sub(n.lastFull) >= time.Minute ||
		!now.Before(n.deadlines[lease.IP]) {
		return false, nil
	}
	config, _ := json.Marshal([]any{n.Interface, n.LANInterface, n.Overlay, n.LocalCIDRs, n.Bypass})
	if n.configuration != string(config) {
		return false, nil
	}
	next, err := leaseMemberships([]model.Lease{lease}, now)
	if err != nil {
		return false, err
	}
	if next[lease.IP] == "" || n.active[lease.IP] != next[lease.IP] {
		return false, nil
	}
	if err = n.inspect(ctx); err != nil {
		return false, err
	}
	// Inspection can take time. Expired elements need the full conntrack fence.
	now = n.now()
	if !now.Before(n.deadlines[lease.IP]) {
		return false, nil
	}
	ttl := int(lease.ExpiresAt.Sub(now).Seconds())
	if ttl <= 0 {
		return false, nil
	}
	if ttl > 120 {
		return false, errors.New("lease exceeds 120 seconds")
	}
	ip, err := netip.ParseAddr(lease.IP)
	if err != nil || !ip.Is4() {
		return false, errors.New("invalid lease IP")
	}
	sets := []string{"active4"}
	if n.Bypass.Enabled {
		for _, resource := range lease.Resources {
			if resource.Enabled && resource.Protocol == "internet" {
				sets = append(sets, "internet4")
				break
			}
		}
	}
	var script strings.Builder
	for _, set := range sets {
		fmt.Fprintf(&script, "delete element inet netgo %s { %s }\n", set, ip)
		fmt.Fprintf(&script, "add element inet netgo %s { %s timeout %ds }\n", set, ip, ttl)
	}
	if err = n.apply(ctx, script.String()); err != nil {
		// A kernel timeout/external deletion can race inspection. The failed
		// atomic batch is followed by full guarded repair, including conntrack.
		n.lastFull = time.Time{}
		if n.pending == nil {
			n.pending = map[string]bool{}
		}
		n.pending[lease.IP] = true
		return false, nil
	}
	n.deadlines[lease.IP] = lease.ExpiresAt
	return true, nil
}

// Unchanged policy needs only fresh timeouts. Flush/repopulate the two sets in
// one nft batch; retain rules/counters and the existing conntrack fence.
func refreshMemberships(leases []model.Lease, internet bool, now time.Time) (string, error) {
	var b strings.Builder
	fmt.Fprintln(&b, "flush set inet netgo active4")
	if internet {
		fmt.Fprintln(&b, "flush set inet netgo internet4")
	}
	for _, l := range leases {
		ttl := int(l.ExpiresAt.Sub(now).Seconds())
		if ttl <= 0 {
			continue
		}
		if ttl > 120 {
			return "", errors.New("lease exceeds 120 seconds")
		}
		ip, err := netip.ParseAddr(l.IP)
		if err != nil || !ip.Is4() {
			return "", errors.New("invalid lease IP")
		}
		fmt.Fprintf(&b, "add element inet netgo active4 { %s timeout %ds }\n", ip, ttl)
		if internet {
			for _, r := range l.Resources {
				if r.Enabled && r.Protocol == "internet" {
					fmt.Fprintf(&b, "add element inet netgo internet4 { %s timeout %ds }\n", ip, ttl)
					break
				}
			}
		}
	}
	return b.String(), nil
}
