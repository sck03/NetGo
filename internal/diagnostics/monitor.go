package diagnostics

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"netgo/internal/controlpath"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type Alert struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type HostResources struct {
	CPUSeconds         float64 `json:"process_cpu_seconds"`
	MaxRSSBytes        uint64  `json:"process_max_rss_bytes"`
	DiskAvailableBytes uint64  `json:"database_disk_available_bytes"`
	DiskTotalBytes     uint64  `json:"database_disk_total_bytes"`
}

type RuntimeReport struct {
	GeneratedAt            time.Time      `json:"generated_at"`
	SampledAt              time.Time      `json:"sampled_at"`
	Version                string         `json:"version"`
	SiteID                 string         `json:"site_id"`
	ConfigRevision         uint64         `json:"config_revision"`
	Status                 string         `json:"status"`
	Checks                 []Check        `json:"checks"`
	Alerts                 []Alert        `json:"alerts"`
	Requests               uint64         `json:"requests"`
	RequestErrors          uint64         `json:"request_errors"`
	AuthenticationFailures int            `json:"authentication_failures_last_minute"`
	UptimeSeconds          int64          `json:"uptime_seconds"`
	HeapBytes              uint64         `json:"process_heap_bytes"`
	Goroutines             int            `json:"process_goroutines"`
	HostResources          HostResources  `json:"host_resources"`
	Counts                 map[string]int `json:"counts"`
	Acceptance             string         `json:"real_network_acceptance"`
}

type Monitor struct {
	mu                 sync.Mutex
	started            time.Time
	sampled            time.Time
	checks             []Check
	resources          HostResources
	requests, failures uint64
	authFailures       []time.Time
	// Probe is injectable so tests never inspect or mutate the host's services.
	Probe func(context.Context, site.Config) ([]Check, HostResources)
}

func NewMonitor() *Monitor { return &Monitor{started: time.Now()} }

func (m *Monitor) Observe(status int, auth bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	if status >= 400 {
		m.failures++
	}
	if auth && status >= 400 && status < 500 {
		m.authFailures = append(m.authFailures, time.Now())
		if len(m.authFailures) > 1024 {
			m.authFailures = m.authFailures[len(m.authFailures)-1024:]
		}
	}
}

func (m *Monitor) Sample(ctx context.Context, c site.Config) {
	probe := m.Probe
	if probe == nil {
		probe = runtimeChecks
	}
	checks, resources := probe(ctx, c)
	m.mu.Lock()
	m.checks, m.resources, m.sampled = checks, resources, time.Now().UTC()
	m.mu.Unlock()
}

func (m *Monitor) Run(ctx context.Context, c site.Config) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 15*time.Second)
		m.Sample(check, c)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) Snapshot(c site.Config, st model.State) RuntimeReport {
	now := time.Now().UTC()
	m.mu.Lock()
	r := RuntimeReport{GeneratedAt: now, SampledAt: m.sampled, Version: model.Version, SiteID: c.Site.ID, ConfigRevision: c.Site.ConfigRevision, Status: "ok", Checks: append([]Check{}, m.checks...), Alerts: []Alert{}, Requests: m.requests, RequestErrors: m.failures, UptimeSeconds: int64(now.Sub(m.started).Seconds()), HostResources: m.resources, Acceptance: "NOT-RUN"}
	for _, at := range m.authFailures {
		if now.Sub(at) < time.Minute {
			r.AuthenticationFailures++
		}
	}
	m.mu.Unlock()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	r.HeapBytes, r.Goroutines = memory.HeapAlloc, runtime.NumGoroutine()
	r.Counts = map[string]int{"users": len(st.Users), "devices": len(st.Devices), "leases": 0, "resources": len(st.Resources)}
	for _, lease := range st.Leases {
		if user, ok := st.Users[lease.UserID]; ok && now.Before(lease.ExpiresAt) && user.Active(now) {
			r.Counts["leases"]++
		}
	}
	add := func(code, severity, message string) {
		r.Alerts = append(r.Alerts, Alert{code, severity, message})
		if severity == "critical" {
			r.Status = "critical"
		} else if r.Status == "ok" {
			r.Status = "warning"
		}
	}
	if r.SampledAt.IsZero() || now.Sub(r.SampledAt) > 90*time.Second {
		add("runtime_checks_stale", "critical", "运行检查尚未完成或已过期，请检查产品服务日志。")
	}
	for _, check := range r.Checks {
		if check.Status == "FAIL" {
			add(check.Name, "critical", check.Detail)
		}
		if check.Status == "WARN" || check.Status == "NOT-RUN" {
			add(check.Name, "warning", check.Detail)
		}
	}
	if st.LastError != "" || st.Revision != st.ObservedRevision {
		add("policy_pending", "warning", "资源策略尚未成功应用，请检查路由节点并重新协调。")
	}
	if r.AuthenticationFailures >= 5 {
		add("repeated_authentication_failures", "warning", "一分钟内出现多次认证失败，请核对账号状态和接入来源。")
	}
	return r
}

func runtimeChecks(ctx context.Context, c site.Config) ([]Check, HostResources) {
	checks := []Check{}
	if runtime.GOOS != "linux" {
		return []Check{{Name: "linux_runtime", Status: "NOT-RUN", Detail: "请在 Debian 12/13 服务端运行系统检查。"}}, HostResources{}
	}
	release, releaseErr := os.ReadFile("/etc/os-release")
	if releaseErr != nil || !SupportedDebianRelease(string(release)) || runtime.GOARCH != "amd64" {
		checks = append(checks, Check{Name: "linux_baseline", Status: "FAIL", Detail: "当前服务端基准为 Debian 12/13 amd64，请核对系统版本。"})
	}
	services := []string{"netbird-server", "netbird-relay", "netgo-router", "netgo-helper", "netgo-caddy"}
	if c.Deployment.Mode == "443" {
		services = append(services, "netgo-haproxy")
	}
	if len(c.Auxiliaries) > 0 {
		services = append(services, "netgo-frpc")
	}
	for _, service := range services {
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := exec.CommandContext(check, "systemctl", "is-active", "--quiet", service).Run()
		cancel()
		status, detail := "PASS", "服务处于活动状态；外部可达性需单独验收。"
		if err != nil {
			status, detail = "FAIL", "服务 "+service+" 未处于活动状态，请检查 systemd 日志。"
		}
		checks = append(checks, Check{Name: service, Status: status, Detail: detail})
	}
	checks = append(checks, CertificateCheck("/etc/netgo/tls/control.crt", c.Site.Origin, time.Now()))
	// Probe actual control responses while preserving canonical TLS identity.
	// A running frpc process alone does not prove the reverse control path works.
	u, _ := url.Parse(c.Site.Origin)
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if c.Deployment.Mode == "443" {
		port = "9443"
	}
	entries := []site.Candidate{{ID: "control_local", Kind: "direct", Address: net.JoinHostPort("127.0.0.1", port)}}
	for _, candidate := range c.Site.Candidates {
		if candidate.Kind == "auxiliary" {
			candidate.ID = "control_auxiliary"
			entries = append(entries, candidate)
		}
	}
	for _, entry := range entries {
		p := c.Site
		entry.Kind = "direct"
		p.Candidates = []site.Candidate{entry}
		selector, err := controlpath.New(p)
		if err == nil {
			check, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, err = selector.Select(check)
			cancel()
		}
		status, detail := "PASS", "同一站点 TLS、协议与健康响应通过；此检查不代表 LAN 数据可达。"
		if err != nil {
			status, detail = "FAIL", "控制入口检查失败，请核对网关、证书和反向通道。"
		}
		checks = append(checks, Check{Name: entry.ID, Status: status, Detail: detail})
	}
	relayPort := c.Deployment.LocalRelayPort
	if relayPort == 0 || c.Deployment.Mode == "443" {
		relayPort = 8443
	}
	connection, relayErr := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
	if connection != nil {
		connection.Close()
	}
	if relayErr != nil {
		checks = append(checks, Check{Name: "local_relay_tcp", Status: "FAIL", Detail: "本地 Relay TCP 监听不可达，请检查独立 Relay 服务。"})
	} else {
		checks = append(checks, Check{Name: "local_relay_tcp", Status: "PASS", Detail: "本地 Relay TCP 监听可达；WS/QUIC 数据转发仍需实测。"})
	}
	resources, err := hostResources(filepath.Dir(c.Database))
	if err != nil {
		checks = append(checks, Check{Name: "host_resources", Status: "FAIL", Detail: "无法读取服务进程或数据库磁盘资源。"})
	} else if resources.DiskTotalBytes == 0 || resources.DiskAvailableBytes < 256<<20 || resources.DiskAvailableBytes < resources.DiskTotalBytes/20 {
		checks = append(checks, Check{Name: "database_disk", Status: "FAIL", Detail: "数据库所在磁盘可用空间不足 5% 或 256 MiB，请释放空间。"})
	} else {
		checks = append(checks, Check{Name: "database_disk", Status: "PASS", Detail: "数据库磁盘可用空间充足。"})
	}
	return checks, resources
}

// CertificateCheck reads the configured public certificate, never its private key.
func CertificateCheck(path, origin string, now time.Time) Check {
	result := Check{Name: "control_certificate", Status: "FAIL", Detail: "控制证书不可读取、域名不匹配或有效期错误，请检查证书部署。"}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return result
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return result
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	u, parseErr := url.Parse(origin)
	if err != nil || parseErr != nil || cert.VerifyHostname(u.Hostname()) != nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return result
	}
	result.Status, result.Detail = "PASS", "控制证书文件域名和有效期正确；实际加载与续期仍需验收。"
	if cert.NotAfter.Sub(now) < 14*24*time.Hour {
		result.Status, result.Detail = "WARN", "控制证书将在 14 天内到期，请检查 DNS-01 续期与服务重载。"
	}
	return result
}

var errUnsupportedResources = errors.New("host resource sampling requires Linux")
