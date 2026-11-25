package node

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/term"
	"io"
	"net"
	"net/netip"
	"net/url"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type terminal struct {
	m Manager
	r *bufio.Reader
}

func (t terminal) text(label, defaultValue string) (string, error) {
	if defaultValue != "" {
		t.m.print("%s [%s]：", label, defaultValue)
	} else {
		t.m.print("%s：", label)
	}
	value, err := t.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultValue
	}
	return value, nil
}
func (t terminal) port(label string, defaultValue int) (int, error) {
	for {
		value, err := t.text(label, strconv.Itoa(defaultValue))
		if err != nil {
			return 0, err
		}
		number, err := strconv.Atoi(value)
		if err == nil && number > 0 && number <= 65535 {
			return number, nil
		}
		t.m.print("请输入 1–65535 的端口号。")
	}
}
func (t terminal) ipv4(label, defaultValue string) (string, error) {
	for {
		value, err := t.text(label, defaultValue)
		if err != nil {
			return "", err
		}
		ip, e := netip.ParseAddr(value)
		if e == nil && ip.Is4() && ip.IsGlobalUnicast() {
			return ip.String(), nil
		}
		t.m.print("请输入 IPv4 主机地址。")
	}
}
func (t terminal) password(label string) (string, error) {
	if t.m.In == os.Stdin && term.IsTerminal(int(os.Stdin.Fd())) {
		t.m.print(label + "：")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		t.m.print("")
		value := string(b)
		clear(b)
		return value, err
	}
	return t.text(label, "")
}
func (t terminal) initialConfig() (site.Config, error) {
	c := site.Config{Site: site.Profile{Protocol: 2, ConfigRevision: 1, ID: "site1", Name: "NetGo"}, Listen: "127.0.0.1:18081", Database: "/var/lib/netgo/netgo.db", ManagementURL: "http://127.0.0.1:18080", ManagementTokenFile: "/etc/netgo/management.token", NativeSecretFile: "/etc/netgo/native.secret", HelperSocket: "/run/netgo-helper/helper.sock", Interface: "wt0", OverlayCIDR: "100.96.0.0/16", LeaseSeconds: 120,
		Deployment: site.Deployment{Mode: "diagnostic", LocalRelayPort: 18443, LocalSTUNPort: 3478, ManagementHealthPort: 19000, ManagementMetricsPort: 19090, RelayHealthPort: 19001, RelayMetricsPort: 19091, CaddyAdminPort: 12019}}
	t.m.print("内网 IP 只能用于内网访问；外网需公网地址/域名及端口映射，或在配置完成后添加 VPS。站点网址同时用作证书身份。")
	host, err := t.text("站点证书域名或服务器 IPv4", "")
	if err != nil {
		return c, err
	}
	relay, err := t.text("Relay 域名或 IPv4", host)
	if err != nil {
		return c, err
	}
	number, err := t.port("控制 HTTPS 端口", 10443)
	if err != nil {
		return c, err
	}
	c.Site.Origin = "https://" + net.JoinHostPort(host, strconv.Itoa(number))
	c.Site.LocalRelay = relay
	c.Site.Candidates = []site.Candidate{{ID: "local", Kind: "direct", Address: net.JoinHostPort(host, strconv.Itoa(number))}}
	if c.Deployment.LocalRelayPort, err = t.port("Relay TCP/UDP 端口", 18443); err != nil {
		return c, err
	}
	if c.Deployment.LocalSTUNPort, err = t.port("STUN UDP 端口", 3478); err != nil {
		return c, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return c, err
	}
	defaultIf := ""
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			defaultIf = iface.Name
			break
		}
	}
	if c.LANInterface, err = t.text("LAN 网卡", defaultIf); err != nil {
		return c, err
	}
	iface, err := net.InterfaceByName(c.LANInterface)
	if err != nil {
		return c, err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return c, err
	}
	networks := []string{}
	for _, address := range addresses {
		prefix, e := netip.ParsePrefix(address.String())
		if e == nil && prefix.Addr().Is4() {
			networks = append(networks, prefix.Masked().String())
		}
	}
	raw, err := t.text("LAN 网段（逗号分隔）", strings.Join(networks, ","))
	if err != nil {
		return c, err
	}
	for _, value := range strings.Split(raw, ",") {
		c.LocalCIDRs = append(c.LocalCIDRs, strings.TrimSpace(value))
	}
	if c.Site.ID, err = t.text("站点 ID", "site1"); err != nil {
		return c, err
	}
	c.Site.Name = "NetGo " + c.Site.ID
	return c, c.Validate()
}
func (t terminal) certificateFiles(c site.Config, role string) (map[string][]byte, error) {
	t.m.print("证书：1) 自动签发（复用已有站点 CA）  2) 导入已有证书  0) 返回")
	choice, err := t.text("请选择", "1")
	if err != nil {
		return nil, err
	}
	if choice == "0" {
		return nil, nil
	}
	source := filepath.Join(t.m.etc(), "tls")
	generate := choice == "1"
	if !generate {
		if choice != "2" {
			return nil, errors.New("无效选项")
		}
		source, err = t.text("证书目录（control.crt/key、relay.crt/key；VPS 用 tunnel.crt/key）", "")
		if err != nil {
			return nil, err
		}
	}
	u, _ := url.Parse(c.Site.Origin)
	hosts := map[string]string{"control": u.Hostname(), "relay": c.Site.LocalRelay}
	if role == "vps" {
		if generate {
			if _, err := os.Stat(filepath.Join(source, "ca.key")); err != nil {
				return nil, errors.New("本站点没有 CA 签发密钥，请导入 VPS 证书")
			}
		}
		if len(c.Auxiliaries) != 1 {
			return nil, errors.New("需要先设置 VPS")
		}
		a := c.Auxiliaries[0]
		u, _ := url.Parse(a.Relay)
		hosts = map[string]string{"relay": u.Hostname(), "tunnel": a.TunnelHost}
	}
	return t.m.certificates(hosts, source, generate)
}
func (t terminal) setup() error {
	if state, err := t.m.installation(); err != nil || state.Status == "uninstalled" {
		return errors.New("请先执行独立安装脚本安装程序")
	}
	if state := new(SetupState); readJSON(filepath.Join(t.m.etc(), "setup.json"), state) == nil {
		if state.Complete {
			return t.m.StartServer()
		}
		if state.Phase == "prepare" {
			t.m.print("继续首次配置。证书：1) 自动生成  2) 导入已有证书")
			choice, err := t.text("请选择", "1")
			if err != nil {
				return err
			}
			source := ""
			if choice == "2" {
				source, err = t.text("证书目录", "")
				if err != nil {
					return err
				}
			} else if choice != "1" {
				return errors.New("无效选项")
			}
			password, err := t.password("管理员密码")
			if err != nil {
				return err
			}
			return t.m.SetupServer(site.Config{}, "", "", password, source, choice == "1")
		}
		password, err := t.password("管理员密码（身份已创建时可留空）")
		if err != nil {
			return err
		}
		return t.m.SetupServer(site.Config{}, "", "", password, "", false)
	}
	c, err := t.initialConfig()
	if err != nil {
		return err
	}
	t.m.print("证书：1) 自动生成内网站点证书  2) 导入已有证书")
	choice, err := t.text("请选择", "1")
	if err != nil {
		return err
	}
	source := ""
	if choice == "2" {
		source, err = t.text("证书目录", "")
		if err != nil {
			return err
		}
	} else if choice != "1" {
		return errors.New("无效选项")
	}
	email, err := t.text("管理员邮箱", "")
	if err != nil {
		return err
	}
	alias, err := t.text("管理员用户名", "admin")
	if err != nil {
		return err
	}
	password, err := t.password("管理员密码")
	if err != nil {
		return err
	}
	confirm, err := t.password("再次输入密码")
	if err != nil {
		return err
	}
	if password != confirm {
		return errors.New("两次密码不一致")
	}
	return t.m.SetupServer(c, email, alias, password, source, choice == "1")
}
func (t terminal) serverMenu() error {
	for {
		t.m.print("\nNetGo 局域网服务端 · ng\n1) 首次配置/启动 VPN\n2) 信息和状态\n3) 修改域名/IP 与端口\n4) 生成/导入证书\n5) 导出客户端配置\n6) 设置旁路由 IP/DNS\n7) VPS 设置与导出\n8) 诊断/恢复连接\n9) 卸载（保留数据）\n10) LAN/公网直达入口\n0) 退出")
		value, err := t.text("请选择", "0")
		if err != nil {
			return err
		}
		if value == "0" {
			return nil
		}
		err = t.serverOperation(value)
		if err != nil {
			t.m.print("操作未完成：%s", err)
		}
	}
}
func (t terminal) serverOperation(value string) error {
	if value == "1" {
		return t.setup()
	}
	if value == "9" {
		answer, err := t.text("输入 yes 卸载受管服务并保留数据", "")
		if err != nil {
			return err
		}
		if answer == "yes" {
			return t.m.Uninstall()
		}
		return nil
	}
	c, err := t.m.config()
	if err != nil {
		return err
	}
	switch value {
	case "2":
		t.m.print("站点：%s\n客户端导入文件：%s\n旁路由：%t", c.Site.Origin, filepath.Join(t.m.etc(), "client-site.json"), c.Bypass.Enabled)
		for _, unit := range wantedUnits(c) {
			t.m.print("%s：%t", unit, t.m.active(unit))
		}
		return nil
	case "10":
		var addresses []string
		for _, candidate := range c.Site.Candidates {
			if candidate.Kind == "direct" {
				addresses = append(addresses, candidate.Address)
			}
		}
		t.m.print("内网 IP 仅在局域网可达。公网直达需公网地址/域名和端口映射；无公网入口时请在菜单 7 配置 VPS。此处不改变站点证书身份。")
		value, e := t.text("直达入口（主机:端口，多个用空格分隔）", strings.Join(addresses, " "))
		if e != nil {
			return e
		}
		next, e := c.Site.WithDirectEndpoints(strings.Fields(value))
		if e != nil {
			return e
		}
		c.Site = next
		return t.m.ApplyServerConfig(c, nil)
	case "3":
		old, _ := url.Parse(c.Site.Origin)
		host, e := t.text("控制域名或 IP", old.Hostname())
		if e != nil {
			return e
		}
		number, e := t.port("控制端口", deploymentPort(old.Port(), 443))
		if e != nil {
			return e
		}
		c.Site = c.Site.ChangeOrigin("https://" + net.JoinHostPort(host, strconv.Itoa(number)))
		if c.Site.LocalRelay, err = t.text("Relay 域名或 IP", c.Site.LocalRelay); err != nil {
			return err
		}
		if c.Deployment.LocalRelayPort, err = t.port("Relay TCP/UDP 端口", c.Deployment.LocalRelayPort); err != nil {
			return err
		}
		if c.Deployment.LocalSTUNPort, err = t.port("STUN UDP 端口", c.Deployment.LocalSTUNPort); err != nil {
			return err
		}
		files, err := t.certificateFiles(c, "server")
		if err != nil || files == nil {
			return err
		}
		c.Site.CA = string(files["ca.crt"])
		return t.m.ApplyServerConfig(c, files)
	case "4":
		files, err := t.certificateFiles(c, "server")
		if err != nil || files == nil {
			return err
		}
		c.Site.CA = string(files["ca.crt"])
		return t.m.ApplyServerConfig(c, files)
	case "5":
		path, err := t.text("客户端配置保存位置", "/root/NetGo-site.json")
		if err != nil {
			return err
		}
		if _, e := os.Stat(path); e == nil {
			return errors.New("文件已存在，请选择新路径")
		}
		return t.m.json(path, c.Site)
	case "6":
		t.m.print("1) 启用/修改旁路由  2) 停用旁路由  0) 返回")
		choice, err := t.text("请选择", "0")
		if err != nil || choice == "0" {
			return err
		}
		if choice == "2" {
			c.Bypass = site.Bypass{}
			c.Site.InternetDNS = nil
		} else if choice == "1" {
			ip, e := t.ipv4("旁路由 IP", c.Bypass.Gateway)
			if e != nil {
				return e
			}
			dns, e := t.ipv4("首选 DNS IP", ip)
			if e != nil {
				return e
			}
			c.Bypass = site.Bypass{Enabled: true, Gateway: ip, DNS: []string{dns}}
			t.m.print("DNS 模式：1) 自动检测  2) 普通 DNS  3) Fake-IP DNS（Clash/Mihomo）")
			dnsMode, e := t.text("请选择", "1")
			if e != nil {
				return e
			}
			if dnsMode == "3" || (dnsMode == "1" && detectFakeIP(dns)) {
				c.Bypass.FakeIPCIDR = "198.18.0.0/15"
				t.m.print("已启用旁路由 Fake-IP 地址支持。")
			} else if dnsMode != "1" && dnsMode != "2" {
				return errors.New("无效 DNS 模式")
			}
			c.Site.InternetDNS = c.Bypass.DNS
			second, e := t.text("备用 DNS IPv4（可留空）", "")
			if e != nil {
				return e
			}
			if second != "" {
				c.Bypass.DNS = append(c.Bypass.DNS, second)
				c.Site.InternetDNS = c.Bypass.DNS
			}
			if _, e = networkSpec(c); e != nil {
				return e
			}
		} else {
			return errors.New("无效选项")
		}
		t.m.print("只有在管理页获得“互联网（旁路由）”资源权限的账号可使用；客户端默认关闭互联网模式。")
		return t.m.ApplyServerConfig(c, nil)
	case "7":
		return t.auxiliaryMenu(c)
	case "8":
		if err = t.m.StartServer(); err != nil {
			return err
		}
		output, err := t.m.command(filepath.Join(t.m.base(), "current/netgoctl"), "doctor", "--config", filepath.Join(t.m.etc(), "site.json"))
		t.m.print("%s", output)
		return err
	default:
		return errors.New("无效选项")
	}
}
func deploymentPort(value string, fallback int) int {
	n, _ := strconv.Atoi(value)
	if n == 0 {
		return fallback
	}
	return n
}
func (m Manager) Menu() error {
	in := m.In
	if in == nil {
		in = os.Stdin
	}
	m.In = in
	t := terminal{m, bufio.NewReader(in)}
	if m.Role == "vps" {
		return t.vpsMenu()
	}
	return t.serverMenu()
}

var _ = json.Valid
var _ = fmt.Sprint
var _ = io.EOF
