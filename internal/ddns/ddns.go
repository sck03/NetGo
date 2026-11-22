package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Config struct {
	ZoneID, RecordID, Name, Type, TokenFile string
	Witnesses                               []string
	IntervalSeconds                         int
	AddressFile                             string
	AddressMaxAgeSeconds                    int
}
type Cloudflare struct {
	Config        Config
	Token         string
	Client        *http.Client
	WitnessClient *http.Client
}

func (c Config) Validate() error {
	if c.Type != "A" && c.Type != "AAAA" {
		return errors.New("DNS type must be A/AAAA")
	}
	if c.ZoneID == "" || c.RecordID == "" || c.Name == "" || c.TokenFile == "" || c.IntervalSeconds < 30 || c.IntervalSeconds > 86400 {
		return errors.New("DNS record, secret file and interval 30..86400 seconds required")
	}
	if c.AddressFile != "" {
		if !filepath.IsAbs(c.AddressFile) || len(c.Witnesses) != 0 || c.AddressMaxAgeSeconds < 0 || c.AddressMaxAgeSeconds > 86400 {
			return errors.New("address file must be absolute, have bounded age and not be combined with witnesses")
		}
		return nil
	}
	if len(c.Witnesses) < 2 || len(c.Witnesses) > 4 {
		return errors.New("two to four independent HTTPS address witnesses required")
	}
	seen := map[string]bool{}
	for _, witness := range c.Witnesses {
		u, err := url.Parse(witness)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return errors.New("invalid witness URL")
		}
		host := strings.ToLower(u.Hostname())
		if seen[host] {
			return errors.New("address witnesses must use distinct hosts")
		}
		seen[host] = true
	}
	return nil
}

// Witnesses must observe the requested address family directly. An HTTP proxy
// would report the proxy address; API requests can still use normal proxy rules.
func New(c Config, token string) Cloudflare {
	rejectRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	family := "tcp4"
	if c.Type == "AAAA" {
		family = "tcp6"
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, _ string, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, family, address)
	}
	return Cloudflare{Config: c, Token: token,
		Client:        &http.Client{Timeout: 10 * time.Second, CheckRedirect: rejectRedirect},
		WitnessClient: &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: rejectRedirect}}
}

func publicAddress(a netip.Addr) bool {
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "100::/64"} {
		if netip.MustParsePrefix(prefix).Contains(a) {
			return false
		}
	}
	return true
}

// Multi-WAN installations can consume a root-managed, periodically refreshed
// per-WAN address file instead of guessing all public IPs from one routed probe.
func (c Config) fileAddress() (netip.Addr, error) {
	info, err := os.Lstat(c.AddressFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128 {
		return netip.Addr{}, errors.New("WAN address file is unavailable or invalid")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0 {
		return netip.Addr{}, errors.New("WAN address file must not be group/world writable")
	}
	maxAge := c.AddressMaxAgeSeconds
	if maxAge == 0 {
		maxAge = 300
	}
	age := time.Since(info.ModTime())
	if age < -30*time.Second || age > time.Duration(maxAge)*time.Second {
		return netip.Addr{}, errors.New("WAN address file is stale; keep the previous DNS record")
	}
	file, err := os.Open(c.AddressFile)
	if err != nil {
		return netip.Addr{}, errors.New("WAN address file unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 129))
	address, parseErr := netip.ParseAddr(strings.TrimSpace(string(data)))
	address = address.Unmap()
	if err != nil || len(data) > 128 || parseErr != nil || !publicAddress(address) || (c.Type == "A") != address.Is4() {
		return netip.Addr{}, errors.New("WAN address file must contain one public IP of the configured family")
	}
	return address, nil
}

func (c Cloudflare) Update(ctx context.Context) (bool, error) {
	if err := c.Config.Validate(); err != nil {
		return false, err
	}
	if c.Client == nil || c.Token == "" {
		return false, errors.New("DNS client and token required")
	}
	probe := c.WitnessClient
	if probe == nil {
		probe = c.Client
	}
	var address netip.Addr
	if c.Config.AddressFile != "" {
		var err error
		address, err = c.Config.fileAddress()
		if err != nil {
			return false, err
		}
	}
	for _, w := range c.Config.Witnesses {
		u, e := url.Parse(w)
		if e != nil || u.Scheme != "https" || u.User != nil {
			return false, errors.New("invalid witness URL")
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", w, nil)
		r, e := probe.Do(req)
		if e != nil {
			return false, errors.New("address witness unavailable")
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, 128))
		r.Body.Close()
		if e != nil || r.StatusCode != 200 {
			return false, errors.New("address witness unavailable")
		}
		a, e := netip.ParseAddr(strings.TrimSpace(string(b)))
		a = a.Unmap()
		if e != nil || !publicAddress(a) {
			return false, errors.New("witness did not return a public address")
		}
		if (c.Config.Type == "A") != a.Is4() {
			return false, errors.New("address family mismatch")
		}
		if address.IsValid() && address != a {
			return false, errors.New("address witnesses disagree")
		}
		address = a
	}
	endpoint := "https://api.cloudflare.com/client/v4/zones/" + url.PathEscape(c.Config.ZoneID) + "/dns_records/" + url.PathEscape(c.Config.RecordID)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	r, e := c.Client.Do(req)
	if e != nil {
		return false, errors.New("DNS API unavailable")
	}
	var existing struct {
		Success bool
		Result  struct {
			Name, Type, Content string
			Proxied             bool
		}
	}
	e = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&existing)
	r.Body.Close()
	if e != nil || r.StatusCode != 200 || !existing.Success || existing.Result.Name != c.Config.Name || existing.Result.Type != c.Config.Type || existing.Result.Proxied {
		return false, errors.New("DNS record mismatch or proxy enabled")
	}
	if existing.Result.Content == address.String() {
		return false, nil
	}
	b, _ := json.Marshal(map[string]any{"type": c.Config.Type, "name": c.Config.Name, "content": address.String(), "ttl": 60, "proxied": false})
	req, _ = http.NewRequestWithContext(ctx, "PATCH", endpoint, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	r, e = c.Client.Do(req)
	if e != nil {
		return false, errors.New("DNS API unavailable")
	}
	defer r.Body.Close()
	var response struct{ Success bool }
	if r.StatusCode != 200 || json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response) != nil || !response.Success {
		return false, errors.New("DNS update failed")
	}
	return true, nil
}
