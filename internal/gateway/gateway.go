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
type NFT struct {
	mu                               sync.Mutex
	Interface, LANInterface, Overlay string
	Run                              func(context.Context, string) error
	Inspect                          func(context.Context) error
	Conntrack                        func(context.Context, string) error
	active                           map[string]string
	pending                          map[string]bool
}

func (n *NFT) Apply(ctx context.Context, leases []model.Lease) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	script, e := Render(n.Interface, n.LANInterface, n.Overlay, leases, time.Now())
	if e != nil {
		return e
	}
	// Refuse to overwrite an unowned table or operate beside flow offload.
	if n.Inspect != nil {
		if e = n.Inspect(ctx); e != nil {
			return e
		}
	} else if n.Run == nil {
		if e = Inspect(ctx, n.Interface, n.LANInterface); e != nil {
			return e
		}
	}
	apply := func(text string) error {
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
	next := map[string]string{}
	for _, lease := range leases {
		if !time.Now().Before(lease.ExpiresAt) {
			continue
		}
		b, err := json.Marshal(lease.Resources)
		if err != nil {
			return err
		}
		next[lease.IP] = model.Hash(lease.ID + string(b))
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
		guard, err := Render(n.Interface, n.LANInterface, n.Overlay, kept, time.Now())
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
	if e = apply(script); e != nil {
		return e
	}
	n.pending = map[string]bool{}
	n.active = next
	return nil
}

const Owner = "NetGo lease gate v1"

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
	fmt.Fprintf(&b, "add table inet netgo { comment %q; }\nflush table inet netgo\n", Owner)
	fmt.Fprintln(&b, "add set inet netgo active4 { type ipv4_addr; flags timeout; }")
	// flush table clears rules, but leaves set elements and their old timeouts.
	// Replace memberships within this same atomic batch on every renewal/revoke.
	fmt.Fprintln(&b, "flush set inet netgo active4")
	fmt.Fprintln(&b, "add chain inet netgo input { type filter hook input priority -150; policy accept; }")
	fmt.Fprintf(&b, "add rule inet netgo input iifname %q counter drop\n", iface)
	fmt.Fprintln(&b, "add chain inet netgo forward { type filter hook forward priority -150; policy accept; }")
	fmt.Fprintln(&b, "add chain inet netgo resources")
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
