package diagnostics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"netgo/internal/controlpath"
	"netgo/internal/gateway"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}
type DoctorReport struct {
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	GeneratedAt time.Time `json:"generated_at"`
	Ready       bool      `json:"ready"`
	Checks      []Check   `json:"checks"`
	Acceptance  string    `json:"real_network_acceptance"`
}

func Doctor(ctx context.Context, c site.Config) DoctorReport {
	out := DoctorReport{OS: runtime.GOOS, Arch: runtime.GOARCH, GeneratedAt: time.Now().UTC(), Ready: true, Acceptance: "NOT-RUN"}
	add := func(name string, err error, detail string) {
		status := "PASS"
		if err != nil {
			status = "FAIL"
			out.Ready = false
		}
		out.Checks = append(out.Checks, Check{Name: name, Status: status, Detail: detail})
	}
	add("config", c.Validate(), "")
	for name, path := range map[string]string{"management_secret": c.ManagementTokenFile, "native_secret": c.NativeSecretFile} {
		_, err := site.Secret(path)
		add(name, err, "protected file readable")
	}
	if runtime.GOOS != "linux" {
		add("linux_host", errors.New("unsupported host"), "Run operational checks on the target Linux host")
	} else {
		data, err := os.ReadFile("/etc/os-release")
		if err == nil && (!SupportedDebianRelease(string(data)) || runtime.GOARCH != "amd64") {
			err = errors.New("unsupported Linux baseline")
		}
		add("linux_distribution", err, "Debian 12/13 amd64 is the current server baseline")
	}
	for _, name := range []string{"nft", "conntrack", "systemctl", "netbird", "openssl", "runuser"} {
		_, err := exec.LookPath(name)
		add("dependency_"+name, err, "")
	}
	_, err := os.Stat("/opt/netgo/upstream/netbird-server")
	add("management_binary", err, "")
	_, err = os.Stat("/opt/netgo/upstream/netbird-relay")
	add("local_relay_binary", err, "")
	_, err = os.Stat("/opt/netgo/upstream/caddy")
	add("control_gateway_binary", err, "")
	for _, name := range []string{c.Interface, c.LANInterface} {
		_, err := net.InterfaceByName(name)
		add("interface_"+name, err, "")
	}
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err == nil && strings.TrimSpace(string(b)) != "1" {
		err = errors.New("forwarding disabled")
	}
	add("ipv4_forwarding", err, "requires net.ipv4.ip_forward=1")
	if runtime.GOOS == "linux" {
		add("firewall_ownership_and_offload", gateway.Inspect(ctx, c.Interface, c.LANInterface), "NetGo table ownership and offload interfaces")
	}
	u, err := url.Parse(c.ManagementURL)
	if err == nil {
		addr := u.Host
		if u.Port() == "" {
			addr = net.JoinHostPort(u.Hostname(), "80")
		}
		conn, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", addr)
		if conn != nil {
			conn.Close()
		}
		add("local_management_tcp", e, "")
	}
	selector, err := controlpath.New(c.Site)
	if err == nil {
		candidate, e := selector.Select(ctx)
		err = e
		if e == nil {
			parsed, _ := url.Parse(c.Site.Origin)
			dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname(), RootCAs: selector.Roots}}
			conn, certErr := dialer.DialContext(ctx, "tcp", candidate.Address)
			if certErr == nil {
				state := conn.(*tls.Conn).ConnectionState()
				conn.Close()
				if len(state.PeerCertificates) > 0 {
					days := int(time.Until(state.PeerCertificates[0].NotAfter).Hours() / 24)
					if days < 14 {
						certErr = errors.New("certificate renewal due")
					}
					add("control_certificate", certErr, "expiry "+state.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339))
				}
			} else {
				add("control_certificate", certErr, "TLS verification failed")
			}
			out.Checks = append(out.Checks, Check{Name: "control_path", Status: "PASS", Detail: candidate.Kind})
		}
	}
	add("trusted_control_entry", err, "health, protocol and site identity; not LAN/data-path acceptance")
	// Inspect local health via the configured loopback address, never a default port.
	request, e := http.NewRequestWithContext(ctx, "GET", "http://"+c.Listen+"/netgo/v1/health", nil)
	if e == nil {
		client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}
		resp, e := client.Do(request)
		if e == nil {
			var health protocol.Health
			e = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&health)
			resp.Body.Close()
			if resp.StatusCode != 200 || health.Validate(c.Site.ID) != nil {
				e = errors.New("wrong local health identity")
			}
		}
		add("local_product_health", e, "")
	}
	return out
}
