// Package deployment renders the reviewed node templates without an interpreter.
package deployment

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"netgo/deploy/templates"
	"netgo/internal/site"
	"regexp"
	"strconv"
	"strings"
)

type Secrets struct {
	Relay  string `json:"relay_secret"`
	FRP    string `json:"frp_secret"`
	Store  string `json:"store_key"`
	Native string `json:"native_secret"`
}

func NewSecrets() (Secrets, error) {
	bytes := make([]byte, 128)
	if _, err := rand.Read(bytes); err != nil {
		return Secrets{}, err
	}
	return Secrets{hex.EncodeToString(bytes[:32]), hex.EncodeToString(bytes[32:64]), base64.StdEncoding.EncodeToString(bytes[64:96]), hex.EncodeToString(bytes[96:])}, nil
}
func (s Secrets) Validate() error {
	for _, value := range []string{s.Relay, s.FRP, s.Native} {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(value) {
			return errors.New("invalid deployment secret")
		}
	}
	b, e := base64.StdEncoding.DecodeString(s.Store)
	if e != nil || len(b) != 32 {
		return errors.New("invalid store key")
	}
	return nil
}

type Node struct {
	Mode               string         `json:"mode"`
	Host               string         `json:"host,omitempty"`
	ControlHost        string         `json:"control_host,omitempty"`
	TunnelHost         string         `json:"tunnel_host,omitempty"`
	ControlPort        int            `json:"control_port"`
	ControlBackendPort int            `json:"control_backend_port"`
	TunnelPort         int            `json:"tunnel_port,omitempty"`
	TunnelBindPort     int            `json:"tunnel_bind_port,omitempty"`
	RelayEndpoint      string         `json:"relay_endpoint"`
	RelayPort          int            `json:"relay_port"`
	RelayBindPort      int            `json:"relay_bind_port"`
	STUNPort           int            `json:"stun_port"`
	Internal           map[string]int `json:"internal_ports,omitempty"`
}
type Rendered struct {
	Local     Node
	Auxiliary *Node
	Files     map[string][]byte
}

func Port(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
func endpoint(origin string) (string, int) {
	u, _ := url.Parse(origin)
	p, _ := strconv.Atoi(u.Port())
	return u.Hostname(), Port(p, 443)
}
func Render(c site.Config, s Secrets) (Rendered, error) {
	out := Rendered{Files: map[string][]byte{}}
	if err := c.Validate(); err != nil {
		return out, err
	}
	if err := s.Validate(); err != nil {
		return out, err
	}
	controlHost, control := endpoint(c.Site.Origin)
	if !validHost(controlHost) || !validHost(c.Site.LocalRelay) {
		return out, errors.New("invalid control or Relay hostname")
	}
	d := c.Deployment
	mode := d.Mode
	if mode == "" {
		mode = "diagnostic"
	}
	relay := Port(d.LocalRelayPort, 8443)
	if mode == "443" {
		if net.ParseIP(controlHost) != nil || net.ParseIP(c.Site.LocalRelay) != nil {
			return out, errors.New("443 sharing requires DNS hostnames for SNI")
		}
		relay = Port(d.LocalRelayPort, 443)
	}
	stun := Port(d.LocalSTUNPort, 3478)
	controlBind, relayBind := control, relay
	if mode == "443" {
		controlBind = 9443
		relayBind = 8443
		if strings.EqualFold(controlHost, c.Site.LocalRelay) {
			return out, errors.New("control and Relay SNI must differ")
		}
	}
	internal := map[string]int{"ManagementHealthPort": Port(d.ManagementHealthPort, 9000), "ManagementMetricsPort": Port(d.ManagementMetricsPort, 9090), "RelayHealthPort": Port(d.RelayHealthPort, 9001), "RelayMetricsPort": Port(d.RelayMetricsPort, 9091), "CaddyAdminPort": Port(d.CaddyAdminPort, 2019)}
	management := strings.TrimPrefix(c.ManagementURL, "http://")
	tcp := []int{controlBind, relayBind}
	for _, addr := range []string{c.Listen, management} {
		_, port, e := net.SplitHostPort(addr)
		if e != nil {
			return out, e
		}
		number, _ := strconv.Atoi(port)
		tcp = append(tcp, number)
	}
	for _, number := range internal {
		tcp = append(tcp, number)
	}
	if mode == "443" {
		tcp = append(tcp, 443)
	}
	if err := distinct(tcp); err != nil {
		return out, err
	}
	if stun == relayBind || stun == relay {
		return out, errors.New("STUN conflicts with Relay UDP")
	}
	out.Local = Node{Mode: mode, ControlPort: control, ControlBackendPort: controlBind, RelayEndpoint: fmt.Sprintf("rels://%s:%d", c.Site.LocalRelay, relay), RelayPort: relay, RelayBindPort: relayBind, STUNPort: stun, Internal: internal}
	values := map[string]string{"CONTROL_HOST": controlHost, "CONTROL_ORIGIN": c.Site.Origin, "CONTROL_PORT": fmt.Sprint(controlBind), "CONTROL_BIND_DIRECTIVE": "", "PRODUCT_LISTEN": c.Listen, "MANAGEMENT_LISTEN": management, "LOCAL_RELAY": c.Site.LocalRelay, "PUBLIC_RELAY_PORT": fmt.Sprint(relay), "RELAY_BIND_PORT": fmt.Sprint(relayBind), "STUN_PORT": fmt.Sprint(stun), "AUX_STUN": "", "AUX_RELAY": "", "RELAY_SECRET": s.Relay, "FRP_SECRET": s.FRP, "STORE_KEY": s.Store}
	if mode == "443" {
		values["CONTROL_BIND_DIRECTIVE"] = " bind 127.0.0.1"
	}
	for key, field := range map[string]string{"MANAGEMENT_HEALTH_PORT": "ManagementHealthPort", "MANAGEMENT_METRICS_PORT": "ManagementMetricsPort", "RELAY_HEALTH_PORT": "RelayHealthPort", "RELAY_METRICS_PORT": "RelayMetricsPort", "CADDY_ADMIN_PORT": "CaddyAdminPort"} {
		values[key] = fmt.Sprint(internal[field])
	}
	if len(c.Auxiliaries) > 0 {
		a := c.Auxiliaries[0]
		u, _ := url.Parse(a.Relay)
		rp, _ := strconv.Atoi(u.Port())
		_, sp, _ := net.SplitHostPort(strings.TrimPrefix(a.STUN, "stun:"))
		st, _ := strconv.Atoi(sp)
		cb, tb, rb := a.PublicControlPort(), a.PublicTunnelPort(), rp
		bind := "0.0.0.0"
		if a.Mode == "443" {
			if net.ParseIP(controlHost) != nil || net.ParseIP(u.Hostname()) != nil || net.ParseIP(a.TunnelHost) != nil {
				return out, errors.New("443 sharing requires three DNS hostnames")
			}
			cb = 24443
			tb = 7000
			rb = 8443
			bind = "127.0.0.1"
			if strings.EqualFold(controlHost, u.Hostname()) || strings.EqualFold(controlHost, a.TunnelHost) {
				return out, errors.New("three distinct auxiliary SNI hostnames required")
			}
		}
		ports := []int{cb, tb, rb, 9001, 9091}
		if a.Mode == "443" {
			ports = append(ports, 443)
		}
		if e := distinct(ports); e != nil {
			return out, e
		}
		if st == rp || st == rb {
			return out, errors.New("auxiliary STUN conflicts with Relay UDP")
		}
		out.Auxiliary = &Node{Mode: a.Mode, Host: a.Host, ControlHost: controlHost, TunnelHost: a.TunnelHost, ControlPort: a.PublicControlPort(), ControlBackendPort: cb, TunnelPort: a.PublicTunnelPort(), TunnelBindPort: tb, RelayEndpoint: a.Relay, RelayPort: rp, RelayBindPort: rb, STUNPort: st}
		expected := net.JoinHostPort(a.Host, fmt.Sprint(a.PublicControlPort()))
		found := false
		for _, candidate := range c.Site.Candidates {
			if candidate.Kind == "auxiliary" {
				if candidate.ID != a.ID || !strings.EqualFold(candidate.Address, expected) {
					return out, errors.New("auxiliary candidate mismatch")
				}
				found = true
			}
		}
		if !found || c.Site.AuxiliaryRelay != a.Relay {
			return out, errors.New("auxiliary profile mismatch")
		}
		for key, value := range map[string]string{"AUX_HOST": a.Host, "AUX_RELAY_HOST": u.Hostname(), "TUNNEL_HOST": a.TunnelHost, "TUNNEL_PORT": fmt.Sprint(a.PublicTunnelPort()), "TUNNEL_BIND_PORT": fmt.Sprint(tb), "REMOTE_CONTROL_PORT": fmt.Sprint(cb), "AUX_RELAY_PORT": fmt.Sprint(rp), "AUX_RELAY_BIND_PORT": fmt.Sprint(rb), "AUX_STUN_PORT": fmt.Sprint(st), "PROXY_BIND": bind, "TUNNEL_BIND": bind, "AUX_STUN": "    - uri: " + strconv.Quote(a.STUN), "AUX_RELAY": "      - " + strconv.Quote(a.Relay)} {
			values[key] = value
		}
	}
	err := fs.WalkDir(templates.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		auxiliary := strings.HasPrefix(path, "auxiliary/")
		if auxiliary && out.Auxiliary == nil {
			return nil
		}
		currentMode := mode
		if auxiliary {
			currentMode = out.Auxiliary.Mode
		}
		if strings.HasSuffix(path, "frpc.toml.tmpl") && out.Auxiliary == nil {
			return nil
		}
		if (strings.HasSuffix(path, "haproxy.cfg.tmpl") || strings.HasSuffix(path, "relay-port.nft.tmpl")) && currentMode != "443" {
			return nil
		}
		content, _ := templates.Files.ReadFile(path)
		text := string(content)
		for key, value := range values {
			text = strings.ReplaceAll(text, "@@"+key+"@@", value)
		}
		if regexp.MustCompile(`@@[A-Z_]+@@`).MatchString(text) {
			return errors.New("unexpanded deployment template")
		}
		out.Files[strings.TrimSuffix(path, ".tmpl")] = []byte(text)
		return nil
	})
	if err != nil {
		return out, err
	}
	if mode == "443" {
		raw, _ := templates.Files.ReadFile("auxiliary/relay-port.nft.tmpl")
		text := strings.ReplaceAll(string(raw), "@@AUX_RELAY_PORT@@", "443")
		text = strings.ReplaceAll(text, "@@AUX_RELAY_BIND_PORT@@", fmt.Sprint(relayBind))
		out.Files["local/relay-port.nft"] = []byte(text)
	}
	out.Files["local/native.secret"] = []byte(s.Native + "\n")
	out.Files["secrets.json"], _ = json.MarshalIndent(s, "", "  ")
	return out, nil
}
func distinct(ports []int) error {
	seen := map[int]bool{}
	for _, p := range ports {
		if p < 1 || p > 65535 || seen[p] {
			return errors.New("listener ports conflict")
		}
		seen[p] = true
	}
	return nil
}

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`).MatchString(label) {
			return false
		}
	}
	return true
}
