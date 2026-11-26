package gateway

import (
	"errors"
	"fmt"
	"net/netip"
	"netgo/internal/model"
	"strings"
	"time"
)

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
