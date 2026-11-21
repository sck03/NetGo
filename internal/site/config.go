package site

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"netgo/internal/model"
	"os"
	"regexp"
	"strings"
)

type Candidate struct {
	ID      string
	Address string
	Kind    string
}
type Profile struct {
	Protocol       int
	ID             string
	Name           string
	Origin         string
	Candidates     []Candidate
	CA             string
	GatewayPeerID  string
	GatewayIP      string
	LocalRelay     string
	AuxiliaryRelay string
}
type Config struct {
	Site                Profile
	Listen              string
	Database            string
	ManagementURL       string
	ManagementTokenFile string
	NativeSecretFile    string
	HelperSocket        string
	Interface           string
	LANInterface        string
	OverlayCIDR         string
	LocalCIDRs          []string
	LeaseSeconds        int
	Auxiliaries         []Auxiliary
}
type Auxiliary struct {
	ID         string
	Host       string
	Relay      string
	STUN       string
	TunnelHost string
	TokenFile  string
	Mode       string
}

var ident = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$")

func ValidID(s string) bool { return ident.MatchString(s) }
func Decode(r io.Reader, dst any) error {
	d := json.NewDecoder(io.LimitReader(r, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}
func Load(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	e = Decode(f, &c)
	if e == nil {
		e = c.Validate()
	}
	return c, e
}
func (p Profile) Validate() error {
	if p.Protocol != model.Protocol || !ValidID(p.ID) {
		return errors.New("unsupported protocol or invalid site ID")
	}
	u, e := url.Parse(p.Origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("origin must be an HTTPS authority")
	}
	if len(p.Candidates) == 0 || len(p.Candidates) > 8 {
		return errors.New("1..8 candidates required")
	}
	seen := map[string]bool{}
	direct := false
	for _, c := range p.Candidates {
		if !ValidID(c.ID) || seen[c.ID] {
			return errors.New("duplicate/invalid candidate ID")
		}
		seen[c.ID] = true
		if c.Kind != "direct" && c.Kind != "auxiliary" {
			return errors.New("invalid candidate kind")
		}
		direct = direct || c.Kind == "direct"
		h, port, e := net.SplitHostPort(c.Address)
		if e != nil || h == "" || port == "" || strings.ContainsAny(h, "/\\\n\r\t \";") {
			return errors.New("candidate must be host:port")
		}
		n, e := net.LookupPort("tcp", port)
		if e != nil || n < 1 || n > 65535 {
			return errors.New("invalid candidate port")
		}
	}
	if !direct {
		return errors.New("local direct candidate is required")
	}
	return nil
}
func (c Config) Validate() error {
	if e := c.Site.Validate(); e != nil {
		return e
	}
	h, _, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return e
	}
	a, e := netip.ParseAddr(h)
	if e != nil || !a.IsLoopback() {
		return errors.New("HTTP service must listen on loopback behind TLS gateway")
	}
	if c.Database == "" || c.HelperSocket == "" || !ValidID(c.Interface) || !ValidID(c.LANInterface) || c.Interface == c.LANInterface {
		return errors.New("invalid paths or network interfaces")
	}
	o, e := netip.ParsePrefix(c.OverlayCIDR)
	if e != nil || !o.Addr().Is4() || o.Bits() < 8 {
		return errors.New("explicit IPv4 overlay CIDR required")
	}
	for _, s := range c.LocalCIDRs {
		p, e := netip.ParsePrefix(s)
		if e != nil || p.Overlaps(o) {
			return fmt.Errorf("invalid/conflicting local network %q", s)
		}
	}
	if c.LeaseSeconds < 15 || c.LeaseSeconds > 120 {
		return errors.New("lease_seconds must be 15..120")
	}
	u, e := url.Parse(c.ManagementURL)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" {
		return errors.New("management URL must be local HTTP")
	}
	ip, e := netip.ParseAddr(u.Hostname())
	if e != nil || !ip.IsLoopback() {
		return errors.New("management API must be loopback")
	}
	if c.ManagementTokenFile == "" || c.NativeSecretFile == "" {
		return errors.New("secret files required")
	}
	if len(c.Auxiliaries) > 1 {
		return errors.New("one auxiliary supported")
	}
	for _, a := range c.Auxiliaries {
		if !ValidID(a.ID) || a.Host == "" || a.TokenFile == "" || (a.Mode != "diagnostic" && a.Mode != "443") {
			return errors.New("invalid auxiliary")
		}
	}
	return nil
}
func ValidateResource(r model.Resource, overlay string) error {
	p, e := netip.ParsePrefix(r.CIDR)
	if e != nil || !p.Addr().Is4() || p != p.Masked() || p.Bits() == 0 || p.Addr().IsLoopback() || p.Addr().IsMulticast() || p.Addr().IsUnspecified() {
		return errors.New("canonical IPv4 LAN CIDR required")
	}
	o, e := netip.ParsePrefix(overlay)
	if e != nil || p.Overlaps(o) {
		return errors.New("resource overlaps overlay")
	}
	if !ValidID(r.ID) || r.Name == "" {
		return errors.New("resource ID/name required")
	}
	if r.Protocol != "tcp" && r.Protocol != "udp" && r.Protocol != "icmp" {
		return errors.New("protocol must be tcp/udp/icmp")
	}
	if r.Protocol != "icmp" && (len(r.Ports) == 0 || len(r.Ports) > 64) {
		return errors.New("1..64 explicit ports required")
	}
	if r.Protocol == "icmp" && len(r.Ports) != 0 {
		return errors.New("icmp has no ports")
	}
	for _, port := range r.Ports {
		if port == 0 {
			return errors.New("port zero forbidden")
		}
	}
	return nil
}
func Secret(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	s := strings.TrimSpace(string(b))
	if len(s) < 32 {
		return "", errors.New("secret shorter than 32 characters")
	}
	return s, nil
}
