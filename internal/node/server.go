package node

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"netgo/internal/deployment"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
)

type SetupState struct {
	Owner        string
	Complete     bool
	Phase        string
	AdminEmail   string
	AdminAlias   string
	AdminID      string
	RouterID     string
	RouterIP     string
	AliasCreated bool
}

var ServerUnits = []string{"netbird-server", "netbird-relay", "netgo-caddy", "netgo-bypass", "netgo-helper", "netgo-router", "netgo-server"}

func wantedUnits(c site.Config) []string {
	units := append([]string{}, ServerUnits...)
	if len(c.Auxiliaries) > 0 {
		units = append(units, "netgo-frpc")
	}
	if c.Deployment.Mode == "443" {
		units = append(units, "netgo-relay-redirect", "netgo-haproxy")
	}
	return units
}
func (m Manager) config() (site.Config, error) { return site.Load(filepath.Join(m.etc(), "site.json")) }
func (m Manager) writeConfig(c site.Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return m.write(filepath.Join(m.etc(), "site.json"), append(b, '\n'), 0640)
}
func (m Manager) API(c site.Config, path, method string, body, out any) error {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequest(method, c.ManagementURL+"/api"+path, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token, e := os.ReadFile(filepath.Join(m.etc(), "management.token")); e == nil {
		request.Header.Set("Authorization", "Token "+strings.TrimSpace(string(token)))
	}
	response, err := localHTTP().Do(request)
	if err != nil {
		return errors.New("本机身份服务暂不可用")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("身份服务 %s %s 返回 HTTP %d", method, path, response.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(out)
	}
	return nil
}
func (m Manager) health(c site.Config) error {
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if c.Site.CA != "" && !roots.AppendCertsFromPEM([]byte(c.Site.CA)) {
		return errors.New("invalid site CA")
	}
	client := localHTTP()
	client.Transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	response, err := client.Get(c.Site.Origin + "/netgo/v2/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("服务健康检查失败")
	}
	return nil
}
func (m Manager) saveSetup(state SetupState) error {
	return m.json(filepath.Join(m.etc(), "setup.json"), state)
}
func (m Manager) renderConfig(c site.Config, secrets deployment.Secrets) error {
	rendered, err := deployment.Render(c, secrets)
	if err != nil {
		return err
	}
	for name, b := range rendered.Files {
		if err = m.write(filepath.Join(m.etc(), "rendered", name), b, 0600); err != nil {
			return err
		}
		if strings.HasPrefix(name, "local/") {
			if err = m.write(filepath.Join(m.etc(), strings.TrimPrefix(name, "local/")), b, 0640); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m Manager) StartServer() error {
	c, err := m.config()
	if err != nil {
		return err
	}
	var state SetupState
	if err = readJSON(filepath.Join(m.etc(), "setup.json"), &state); err != nil {
		return err
	}
	if state.Owner != Owner || !state.Complete {
		return errors.New("请先完成首次配置")
	}
	if err = m.Forwarding(); err != nil {
		return err
	}
	for _, unit := range wantedUnits(c) {
		if err = m.service("start", unit); err != nil {
			return err
		}
	}
	if err = wait("VPN TLS 服务", func() error { return m.health(c) }); err != nil {
		return err
	}
	return m.connectRouter(c, state)
}
func (m Manager) router(args ...string) ([]byte, error) {
	prefix := []string{"--config", m.path("/var/lib/netgo-router/config.json"), "--daemon-addr", "unix://" + m.path("/run/netgo-router/netbird.sock")}
	return m.command(filepath.Join(m.base(), "upstream/netbird"), append(prefix, args...)...)
}
func (m Manager) connectRouter(c site.Config, state SetupState) error {
	if state.RouterID == "" || state.RouterIP == "" {
		return errors.New("路由节点身份缺失")
	}
	if _, err := m.router("up", "--management-url", c.Site.Origin, "--disable-auto-connect=false"); err != nil {
		return err
	}
	return wait("路由节点连接", func() error {
		var p struct {
			Connected bool   `json:"connected"`
			IP        string `json:"ip"`
		}
		if err := m.API(c, "/peers/"+url.PathEscape(state.RouterID), "GET", nil, &p); err != nil {
			return err
		}
		if !p.Connected || p.IP != state.RouterIP {
			return errors.New("router is not connected")
		}
		return nil
	})
}
func (m Manager) SetupServer(c site.Config, email, alias, password, tlsSource string, generate bool) (result error) {
	installed, err := m.installation()
	if err != nil {
		return err
	}
	if err = m.verifyInstalled(installed); err != nil {
		return err
	}
	var state SetupState
	if err = readJSON(filepath.Join(m.etc(), "setup.json"), &state); err == nil {
		if state.Owner != Owner {
			return errors.New("配置归属未知")
		}
		if state.Complete {
			return m.StartServer()
		}
		c, err = m.config()
		if err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if c.Database != "/var/lib/netgo/netgo.db" || c.ManagementTokenFile != "/etc/netgo/management.token" || c.NativeSecretFile != "/etc/netgo/native.secret" || c.HelperSocket != "/run/netgo-helper/helper.sock" {
			return errors.New("一键配置必须使用本项目的受管数据和密钥路径")
		}
		if c.Deployment.Mode == "443" || len(c.Auxiliaries) > 0 {
			return errors.New("首次安装先配置本地独立端口，随后在菜单添加 VPS")
		}
		if err = c.Validate(); err != nil {
			return err
		}
		rendered, err := deployment.Render(c, deployment.Secrets{Relay: strings.Repeat("a", 64), FRP: strings.Repeat("b", 64), Store: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", Native: strings.Repeat("c", 64)})
		if err != nil {
			return err
		}
		tcp := []int{rendered.Local.ControlBackendPort, rendered.Local.RelayBindPort}
		for _, p := range rendered.Local.Internal {
			tcp = append(tcp, p)
		}
		for _, addr := range []string{c.Listen, strings.TrimPrefix(c.ManagementURL, "http://")} {
			u, _ := url.Parse("http://" + addr)
			p := u.Port()
			number := 0
			fmt.Sscan(p, &number)
			tcp = append(tcp, number)
		}
		if err = checkPorts(tcp, []int{rendered.Local.RelayBindPort, rendered.Local.STUNPort, 51820}); err != nil {
			return err
		}
		if !strings.Contains(email, "@") || !site.ValidID(alias) {
			return errors.New("需要有效管理员邮箱和用户名")
		}
		state = SetupState{Owner: Owner, Phase: "prepare", AdminEmail: email, AdminAlias: alias}
		if err = m.writeConfig(c); err != nil {
			return err
		}
		if err = m.saveSetup(state); err != nil {
			return err
		}
		u, _ := url.Parse(c.Site.Origin)
		files, err := m.certificates(map[string]string{"control": u.Hostname(), "relay": c.Site.LocalRelay}, tlsSource, generate)
		if err != nil {
			return err
		}
		for name, b := range files {
			mode := os.FileMode(0640)
			if name == "ca.key" {
				mode = 0600
			}
			if err = m.write(filepath.Join(m.etc(), "tls", name), b, mode); err != nil {
				return err
			}
		}
		c.Site.CA = string(files["ca.crt"])
		if err = m.writeConfig(c); err != nil {
			return err
		}
		secrets, err := deployment.NewSecrets()
		if err != nil {
			return err
		}
		if err = m.renderConfig(c, secrets); err != nil {
			return err
		}
		if err = m.write(m.path("/var/lib/netgo-router/config.json"), []byte(`{"DisableAutoConnect":true,"ManagementURL":{"Scheme":"https","Host":"127.0.0.1:1"}}`), 0600); err != nil {
			return err
		}
		if err = m.dataDirectory(m.path("/var/lib/netgo"), "netgo"); err != nil {
			return err
		}
		state.Phase = "configured"
		if err = m.saveSetup(state); err != nil {
			return err
		}
	} else {
		return err
	}
	if state.Phase == "prepare" {
		return errors.New("证书准备曾中断，保留诊断后重新执行证书设置")
	}
	if state.AdminID == "" && (len([]byte(password)) < 8 || len([]byte(password)) > 72) {
		return errors.New("管理员密码需要 8–72 UTF-8 字节")
	}
	defer func() {
		if result != nil {
			for i := len(ServerUnits) - 1; i >= 0; i-- {
				_ = m.service("stop", ServerUnits[i])
			}
			_ = m.RestoreForwarding()
			_ = m.saveSetup(state)
		}
	}()
	bootstrap := "NB_SETUP_PAT_ENABLED=false\n"
	if state.AdminID == "" {
		bootstrap = "NB_SETUP_PAT_ENABLED=true\n"
	}
	if err = m.write(filepath.Join(m.etc(), "bootstrap.env"), []byte(bootstrap), 0600); err != nil {
		return err
	}
	if err = m.service("start", "netbird-server", "netgo-caddy", "netbird-relay"); err != nil {
		return err
	}
	if err = wait("本机身份服务", func() error { var out any; return m.API(c, "/instance", "GET", nil, &out) }); err != nil {
		return err
	}
	if state.AdminID == "" {
		var instance struct {
			SetupRequired bool `json:"setup_required"`
		}
		if err = m.API(c, "/instance", "GET", nil, &instance); err != nil {
			return err
		}
		if !instance.SetupRequired {
			return errors.New("身份已初始化但缺少本站点归属记录")
		}
		var created struct {
			ID    string `json:"user_id"`
			Token string `json:"personal_access_token"`
		}
		body := map[string]any{"email": state.AdminEmail, "name": state.AdminAlias, "password": password, "create_pat": true, "pat_expire_in": 365}
		if err = m.API(c, "/setup", "POST", body, &created); err != nil {
			return err
		}
		password = ""
		if created.ID == "" || created.Token == "" {
			return errors.New("初始化未返回管理员身份")
		}
		if err = m.write(filepath.Join(m.etc(), "management.token"), []byte(created.Token+"\n"), 0640); err != nil {
			return err
		}
		state.AdminID = created.ID
		state.Phase = "identity"
		if err = m.saveSetup(state); err != nil {
			return err
		}
	}
	if err = m.write(filepath.Join(m.etc(), "bootstrap.env"), []byte("NB_SETUP_PAT_ENABLED=false\n"), 0600); err != nil {
		return err
	}
	if err = m.configureIDP(c, state); err != nil {
		return err
	}
	if err = m.service("start", "netgo-bypass", "netgo-helper", "netgo-router"); err != nil {
		return err
	}
	if err = wait("路由 IPC", func() error { _, e := os.Stat(m.path("/run/netgo-router/netbird.sock")); return e }); err != nil {
		return err
	}
	if state.RouterID == "" {
		if err = m.enrollRouter(c, &state); err != nil {
			return err
		}
		if err = m.saveSetup(state); err != nil {
			return err
		}
	}
	if err = m.connectRouter(c, state); err != nil {
		return err
	}
	c.Site.GatewayPeerID = state.RouterID
	c.Site.GatewayIP = state.RouterIP
	if err = m.writeConfig(c); err != nil {
		return err
	}
	ctl := filepath.Join(m.base(), "current/netgoctl")
	if !state.AliasCreated {
		if _, err = m.command("runuser", "-u", "netgo", "--", ctl, "init-admin", "--config", filepath.Join(m.etc(), "site.json"), "--admin-id", state.AdminID, "--admin-alias", state.AdminAlias, "--admin-email", state.AdminEmail); err != nil {
			return err
		}
		state.AliasCreated = true
		if err = m.saveSetup(state); err != nil {
			return err
		}
	}
	if err = m.Forwarding(); err != nil {
		return err
	}
	if _, err = m.command("runuser", "-u", "netgo", "--", ctl, "reconcile", "--config", filepath.Join(m.etc(), "site.json")); err != nil {
		return err
	}
	if err = m.service("restart", "netbird-server", "netgo-helper", "netgo-server"); err != nil {
		return err
	}
	if err = wait("VPN TLS 健康", func() error { return m.health(c) }); err != nil {
		return err
	}
	if err = m.service("enable", ServerUnits...); err != nil {
		return err
	}
	state.Complete = true
	state.Phase = "ready"
	if err = m.saveSetup(state); err != nil {
		return err
	}
	if err = m.json(filepath.Join(m.etc(), "client-site.json"), c.Site); err != nil {
		return err
	}
	m.print("VPN 已初始化：%s\n管理员：%s\n客户端导入文件：%s", c.Site.Origin, state.AdminAlias, filepath.Join(m.etc(), "client-site.json"))
	return nil
}
