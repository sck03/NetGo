package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

type Config struct {
	ZoneID, RecordID, Name, Type, TokenFile string
	Witnesses                               []string
	IntervalSeconds                         int
}
type Cloudflare struct {
	Config Config
	Token  string
	Client *http.Client
}

func (c Cloudflare) Update(ctx context.Context) (bool, error) {
	if c.Config.Type != "A" && c.Config.Type != "AAAA" {
		return false, errors.New("DNS type must be A/AAAA")
	}
	if len(c.Config.Witnesses) < 2 {
		return false, errors.New("two independent HTTPS address witnesses required")
	}
	var address netip.Addr
	for _, w := range c.Config.Witnesses {
		u, e := url.Parse(w)
		if e != nil || u.Scheme != "https" || u.User != nil {
			return false, errors.New("invalid witness URL")
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", w, nil)
		r, e := c.Client.Do(req)
		if e != nil {
			return false, e
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, 128))
		r.Body.Close()
		if e != nil || r.StatusCode != 200 {
			return false, errors.New("address witness unavailable")
		}
		a, e := netip.ParseAddr(strings.TrimSpace(string(b)))
		if e != nil || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(a) {
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
		return false, e
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
		return false, e
	}
	defer r.Body.Close()
	var response struct{ Success bool }
	if r.StatusCode != 200 || json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response) != nil || !response.Success {
		return false, errors.New("DNS update failed")
	}
	return true, nil
}
