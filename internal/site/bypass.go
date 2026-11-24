package site

import (
	"errors"
	"net/netip"
)

// Bypass is an opt-in IPv4 Internet egress through a separate LAN router.
// LAN permissions and DNS destinations remain explicit at the lease gate.
type Bypass struct {
	Enabled    bool
	Gateway    string
	DNS        []string
	FakeIPCIDR string `json:",omitempty"`
}

func ValidateDNS(servers []string) error {
	if len(servers) > 3 {
		return errors.New("at most three IPv4 DNS servers required")
	}
	seen := map[string]bool{}
	for _, value := range servers {
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || seen[value] {
			return errors.New("invalid/duplicate IPv4 DNS server")
		}
		seen[value] = true
	}
	return nil
}
func (b Bypass) Validate(local []string, overlay string) error {
	if !b.Enabled {
		if b.Gateway != "" || len(b.DNS) != 0 || b.FakeIPCIDR != "" {
			return errors.New("disabled bypass must have no gateway or DNS")
		}
		return nil
	}
	if len(b.DNS) == 0 {
		return errors.New("bypass DNS required")
	}
	if b.FakeIPCIDR != "" && b.FakeIPCIDR != "198.18.0.0/15" {
		return errors.New("only the dedicated 198.18.0.0/15 Fake-IP DNS range is supported")
	}
	if err := ValidateDNS(b.DNS); err != nil {
		return err
	}
	ip, err := netip.ParseAddr(b.Gateway)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() {
		return errors.New("invalid bypass gateway")
	}
	pool, err := netip.ParsePrefix(overlay)
	if err != nil || pool.Contains(ip) {
		return errors.New("bypass gateway overlaps overlay")
	}
	found := false
	for _, value := range local {
		network, err := netip.ParsePrefix(value)
		if err != nil {
			return err
		}
		if network.Contains(ip) && network.Bits() < 31 && ip != network.Masked().Addr() {
			last := network.Masked().Addr().As4()
			candidate := ip.As4()
			networkValue := uint32(last[0])<<24 | uint32(last[1])<<16 | uint32(last[2])<<8 | uint32(last[3])
			ipValue := uint32(candidate[0])<<24 | uint32(candidate[1])<<16 | uint32(candidate[2])<<8 | uint32(candidate[3])
			found = ipValue != networkValue|(^uint32(0)>>network.Bits())
		}
	}
	if !found {
		return errors.New("bypass gateway must be a host on the configured LAN")
	}
	return nil
}
