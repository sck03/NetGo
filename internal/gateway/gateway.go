package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Gate interface {
	Apply(context.Context, []model.Lease) error
}

// Renewer can extend an installed, unchanged membership. False requests a
// fenced full apply (restart, expiry, policy change or periodic repair).
type Renewer interface {
	Renew(context.Context, model.Lease) (bool, error)
}
type NFT struct {
	mu                               sync.Mutex
	Interface, LANInterface, Overlay string
	Bypass                           site.Bypass
	LocalCIDRs                       []string
	Run                              func(context.Context, string) error
	Inspect                          func(context.Context) error
	Conntrack                        func(context.Context, string) error
	Now                              func() time.Time
	active                           map[string]string
	pending                          map[string]bool
	lastFull                         time.Time
	configuration                    string
	deadlines                        map[string]time.Time
}

func (n *NFT) Apply(ctx context.Context, leases []model.Lease) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var e error
	// Refuse to overwrite an unowned table or operate beside flow offload.
	if e = n.inspect(ctx); e != nil {
		return e
	}
	apply := func(text string) error { return n.apply(ctx, text) }
	now := n.now()
	next, e := leaseMemberships(leases, now)
	if e != nil {
		return e
	}
	config, _ := json.Marshal([]any{n.Interface, n.LANInterface, n.Overlay, n.LocalCIDRs, n.Bypass})
	if len(n.pending) == 0 && n.configuration == string(config) && !n.lastFull.IsZero() && now.Sub(n.lastFull) < time.Minute && reflect.DeepEqual(n.active, next) {
		script, err := refreshMemberships(leases, n.Bypass.Enabled, n.now())
		if err != nil {
			return err
		}
		if err = apply(script); err != nil {
			n.lastFull = time.Time{}
			return err
		}
		n.rememberDeadlines(leases)
		return nil
	}
	script, e := RenderBypass(n.Interface, n.LANInterface, n.Overlay, n.LocalCIDRs, n.Bypass, leases, now)
	if e != nil {
		return e
	}
	if n.pending == nil {
		n.pending = map[string]bool{}
	}
	for ip, old := range n.active {
		if next[ip] != old {
			n.pending[ip] = true
		}
	}
	// A new IP may still have tracked flows from an expired lease or a helper
	// restart. Always clear that exact scope before opening it again.
	for ip, current := range next {
		if n.active[ip] != current {
			n.pending[ip] = true
		}
	}
	if len(n.pending) != 0 {
		kept := []model.Lease{}
		for _, l := range leases {
			if !n.pending[l.IP] {
				kept = append(kept, l)
			}
		}
		guard, err := RenderBypass(n.Interface, n.LANInterface, n.Overlay, n.LocalCIDRs, n.Bypass, kept, n.now())
		if err != nil {
			return err
		}
		if err = apply(guard); err != nil {
			return err
		}
		ips := make([]string, 0, len(n.pending))
		for ip := range n.pending {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		for _, ip := range ips {
			if n.Conntrack != nil {
				e = n.Conntrack(ctx, ip)
			} else if n.Run == nil {
				e = ClearConntrack(ctx, ip)
			}
			if e != nil {
				return e
			}
		}
	}
	// Cleanup can outlast a lease. Rebuild relative deadlines before opening it.
	now = n.now()
	script, e = RenderBypass(n.Interface, n.LANInterface, n.Overlay, n.LocalCIDRs, n.Bypass, leases, now)
	if e != nil {
		return e
	}
	next, e = leaseMemberships(leases, now)
	if e != nil {
		return e
	}
	if e = apply(script); e != nil {
		return e
	}
	n.pending = map[string]bool{}
	n.active = next
	n.lastFull = now
	n.configuration = string(config)
	n.rememberDeadlines(leases)
	return nil
}

func (n *NFT) inspect(ctx context.Context) error {
	if n.Inspect != nil {
		return n.Inspect(ctx)
	}
	if n.Run == nil {
		return Inspect(ctx, n.Interface, n.LANInterface)
	}
	return nil
}

func (n *NFT) apply(ctx context.Context, text string) error {
	if n.Run != nil {
		return n.Run(ctx, text)
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nft apply failed: %w", err)
	}
	return nil
}

func (n *NFT) rememberDeadlines(leases []model.Lease) {
	n.deadlines = make(map[string]time.Time, len(leases))
	for _, lease := range leases {
		n.deadlines[lease.IP] = lease.ExpiresAt
	}
}

const Owner = "NetGo lease gate v1"

func (n *NFT) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func leaseMemberships(leases []model.Lease, now time.Time) (map[string]string, error) {
	next := make(map[string]string, len(leases))
	for _, lease := range leases {
		// Match the renderer's whole-second kernel timeout.
		if lease.ExpiresAt.Sub(now) < time.Second {
			continue
		}
		b, err := json.Marshal(lease.Resources)
		if err != nil {
			return nil, err
		}
		if _, exists := next[lease.IP]; exists {
			return nil, errors.New("duplicate lease IP")
		}
		next[lease.IP] = model.Hash(lease.ID + string(b))
	}
	return next, nil
}

// Inspect is read-only. Offloaded flows can bypass the ordinary forward hook.
// The administrator must disable conflicting offload before starting NetGo.
func Inspect(ctx context.Context, iface, lan string) error {
	b, err := exec.CommandContext(ctx, "nft", "-j", "list", "ruleset").Output()
	if err != nil {
		return errors.New("cannot inspect host firewall")
	}
	// nftables 1.0.6 omits table comments from JSON, although the kernel
	// retains them. Verify its explicit text representation before enriching
	// the document; a rule/chain comment never establishes table ownership.
	var rules struct {
		Objects []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(b, &rules); err != nil {
		return err
	}
	for _, object := range rules.Objects {
		raw, ok := object["table"]
		if !ok {
			continue
		}
		var table map[string]any
		if err := json.Unmarshal(raw, &table); err != nil {
			return err
		}
		if table["family"] == "inet" && table["name"] == "netgo" && table["comment"] == nil {
			text, err := exec.CommandContext(ctx, "nft", "list", "table", "inet", "netgo").Output()
			if err != nil || !legacyOwnedTable(text) {
				return errors.New("inet netgo table is not owned by NetGo")
			}
			table["comment"] = Owner
			object["table"], err = json.Marshal(table)
			if err != nil {
				return err
			}
		}
	}
	b, err = json.Marshal(rules)
	if err != nil {
		return err
	}
	return ValidateRuleset(b, iface, lan)
}

func legacyOwnedTable(text []byte) bool {
	lines := strings.Split(strings.TrimSpace(string(text)), "\n")
	return len(lines) >= 3 && strings.TrimSpace(lines[0]) == "table inet netgo {" &&
		strings.TrimSpace(lines[1]) == "comment "+strconv.Quote(Owner)
}
func ValidateRuleset(b []byte, iface, lan string) error {
	var rules struct {
		Objects []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(b, &rules); err != nil {
		return err
	}
	if rules.Objects == nil {
		return errors.New("invalid nftables ruleset document")
	}
	for _, object := range rules.Objects {
		if raw, ok := object["table"]; ok {
			var table struct{ Family, Name, Comment string }
			if err := json.Unmarshal(raw, &table); err != nil {
				return err
			}
			if table.Family == "inet" && table.Name == "netgo" && table.Comment != Owner {
				return errors.New("inet netgo table is not owned by NetGo")
			}
		}
		if raw, ok := object["flowtable"]; ok {
			var flow struct {
				Devices json.RawMessage `json:"dev"`
			}
			if err := json.Unmarshal(raw, &flow); err != nil {
				return err
			}
			var devices []string
			if json.Unmarshal(flow.Devices, &devices) != nil {
				var device string
				if json.Unmarshal(flow.Devices, &device) != nil {
					return errors.New("cannot determine flow offload interfaces")
				}
				devices = []string{device}
			}
			for _, device := range devices {
				if device == iface || device == lan {
					return errors.New("flow offload conflicts with lease gate")
				}
			}
		}
	}
	return nil
}
func ClearConntrack(ctx context.Context, ip string) error {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return errors.New("invalid conntrack source")
	}
	for _, direction := range []string{"--orig-src", "--orig-dst"} {
		command := exec.CommandContext(ctx, "conntrack", "--delete", "--family", "ipv4", direction, a.String())
		command.Env = append(os.Environ(), "LC_ALL=C")
		output, runErr := command.CombinedOutput()
		err = runErr
		if err != nil {
			var status *exec.ExitError
			// conntrack returns 1 when this exact scope has no entries.
			if !errors.As(err, &status) || status.ExitCode() != 1 || ctx.Err() != nil || !noConntrackEntries(output) {
				return fmt.Errorf("scoped conntrack cleanup failed: %w", err)
			}
		}
	}
	return nil
}
func noConntrackEntries(output []byte) bool {
	// Exit 1 alone also covers permission/kernel failures. Only the exact
	// zero-deleted summary is a successful empty scope.
	return regexp.MustCompile(`^conntrack v[^\r\n]+: 0 flow entries have been deleted\.[\r\n]*$`).Match(output)
}

// RenderBypass replaces only the owned table, atomically. Timeouts remain effective if
// both the application and helper crash. The early drop also covers established flows.
func RenderBypass(iface, lan, overlay string, local []string, bypass site.Bypass, leases []model.Lease, now time.Time) (string, error) {
	if err := bypass.Validate(local, overlay); err != nil {
		return "", err
	}
	if !site.ValidID(iface) || !site.ValidID(lan) || iface == lan {
		return "", errors.New("invalid interface")
	}
	pool, e := netip.ParsePrefix(overlay)
	if e != nil || !pool.Addr().Is4() {
		return "", errors.New("invalid overlay")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "add table inet netgo { comment %q; }\nflush table inet netgo\n", Owner)
	fmt.Fprintln(&b, "add set inet netgo active4 { type ipv4_addr; flags timeout; }")
	// flush table clears rules, but leaves set elements and their old timeouts.
	// Replace memberships within this same atomic batch on every renewal/revoke.
	fmt.Fprintln(&b, "flush set inet netgo active4")
	fmt.Fprintln(&b, "add chain inet netgo input { type filter hook input priority -150; policy accept; }")
	fmt.Fprintln(&b, "add chain inet netgo forward { type filter hook forward priority -150; policy accept; }")
	fmt.Fprintln(&b, "add chain inet netgo resources")
	fmt.Fprintf(&b, "add rule inet netgo input iifname %q ip saddr @active4 jump resources\n", iface)
	fmt.Fprintf(&b, "add rule inet netgo input iifname %q counter drop\n", iface)
	if bypass.Enabled {
		fmt.Fprintf(&b, "add set inet netgo nonpublic4 { type ipv4_addr; flags interval; auto-merge; }\nflush set inet netgo nonpublic4\nadd element inet netgo nonpublic4 { %s }\n", strings.Join(append(append([]string(nil), NonPublicIPv4...), local...), ", "))
		fmt.Fprintln(&b, "add set inet netgo internet4 { type ipv4_addr; flags timeout; }\nflush set inet netgo internet4")
		fmt.Fprintln(&b, "add chain inet netgo internet")
		for _, dns := range bypass.DNS {
			fmt.Fprintf(&b, "add rule inet netgo internet ip daddr %s meta l4proto { tcp, udp } th dport 53 counter accept\n", dns)
		}
		if bypass.FakeIPCIDR != "" {
			fmt.Fprintf(&b, "add rule inet netgo internet ip daddr %s meta l4proto { tcp, udp } counter accept\n", bypass.FakeIPCIDR)
		}
		fmt.Fprintln(&b, "add rule inet netgo internet ip daddr @nonpublic4 return\nadd rule inet netgo internet meta l4proto { tcp, udp, icmp } counter accept")
		fmt.Fprintln(&b, "add chain inet netgo egress { type nat hook postrouting priority 99; policy accept; }")
		fmt.Fprintf(&b, "add rule inet netgo egress iifname %q oifname %q ip saddr @internet4 masquerade\n", iface, lan)
	}
	fmt.Fprintf(&b, "add rule inet netgo forward iifname %q oifname %q ip saddr @active4 jump resources\n", iface, lan)
	fmt.Fprintf(&b, "add rule inet netgo forward iifname %q counter drop\n", iface)
	fmt.Fprintf(&b, "add rule inet netgo forward oifname %q ip daddr @active4 ct state established,related accept\n", iface)
	fmt.Fprintf(&b, "add rule inet netgo forward oifname %q counter drop\n", iface)
	seen := map[string]bool{}
	leases = append([]model.Lease(nil), leases...)
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
		internetAdded := false
		for _, r := range l.Resources {
			if !r.Enabled {
				continue
			}
			if e := site.ValidateResource(r, overlay); e != nil {
				return "", e
			}
			if r.Protocol == "internet" {
				if bypass.Enabled && !internetAdded {
					fmt.Fprintf(&b, "add element inet netgo internet4 { %s timeout %ds }\nadd rule inet netgo resources ip saddr %s jump internet\n", ip, ttl, ip)
					internetAdded = true
				}
				continue
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
