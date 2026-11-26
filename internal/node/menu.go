package node

import (
	"bufio"
	"errors"
	"golang.org/x/term"
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
func (t terminal) serverOperation(value string) error {
	if value == "setup" {
		return t.setup()
	}
	if value == "uninstall" {
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
	case "status":
		return t.serverStatus(c)
	case "endpoints":
		var addresses []string
		for _, candidate := range c.Site.Candidates {
			if candidate.Kind == "direct" {
				addresses = append(addresses, candidate.Address)
			}
		}
		t.m.print("内网 IP 仅在局域网可达。公网直达需公网地址/域名和端口映射；无公网入口时请选择“设置外网访问 → 配对 / 管理 VPS”。此处不改变站点证书身份。")
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
	case "identity":
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
	case "certificates":
		files, err := t.certificateFiles(c, "server")
		if err != nil || files == nil {
			return err
		}
		c.Site.CA = string(files["ca.crt"])
		return t.m.ApplyServerConfig(c, files)
	case "export":
		if err := c.Site.Validate(); err != nil {
			return errors.New("站点网关尚未就绪，请先完成服务器初始化")
		}
		path, err := t.text("客户端配置保存位置", "/root/NetGo-site.json")
		if err != nil {
			return err
		}
		if _, e := os.Stat(path); e == nil {
			return errors.New("文件已存在，请选择新路径")
		}
		if err = t.m.json(path, c.Site); err != nil {
			return err
		}
		t.m.print("站点文件已保存：%s。将它交给用户，在客户端选择导入后登录。账号与资源权限在管理网页设置。", path)
		return nil
	case "bypass":
		c, err = t.editBypass(c, lookupDNS)
		if err != nil {
			return err
		}
		if _, err = networkSpec(c); err != nil {
			return err
		}
		t.m.print("只有在管理页获得“互联网（旁路由）”资源权限的账号可使用；客户端默认关闭互联网模式。")
		return t.m.ApplyServerConfig(c, nil)
	case "check-network":
		return t.m.CheckNetwork(c)
	case "auxiliary":
		return t.auxiliaryMenu(c)
	case "repair":
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
