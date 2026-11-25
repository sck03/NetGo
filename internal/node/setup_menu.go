package node

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"netgo/internal/site"
	"strconv"
	"strings"
)

type lanNetwork struct{ name, address, cidr string }

func discoverLANs() ([]lanNetwork, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []lanNetwork
	overlay := netip.MustParsePrefix("100.96.0.0/16")
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() || prefix.Bits() == 0 || prefix.Overlaps(overlay) {
				continue
			}
			result = append(result, lanNetwork{iface.Name, prefix.Addr().String(), prefix.Masked().String()})
		}
	}
	return result, nil
}

func (t terminal) initialConfig() (site.Config, error) {
	networks, err := discoverLANs()
	if err != nil {
		return site.Config{}, err
	}
	return t.initialConfigWithNetworks(networks)
}

func (t terminal) initialConfigWithNetworks(networks []lanNetwork) (site.Config, error) {
	c := site.Config{Site: site.Profile{Protocol: 2, ConfigRevision: 1}, Listen: "127.0.0.1:18081", Database: "/var/lib/netgo/netgo.db", ManagementURL: "http://127.0.0.1:18080", ManagementTokenFile: "/etc/netgo/management.token", NativeSecretFile: "/etc/netgo/native.secret", HelperSocket: "/run/netgo-helper/helper.sock", Interface: "wt0", OverlayCIDR: "100.96.0.0/16", LeaseSeconds: 120,
		Deployment: site.Deployment{Mode: "diagnostic", LocalRelayPort: 18443, LocalSTUNPort: 3478, ManagementHealthPort: 19000, ManagementMetricsPort: 19090, RelayHealthPort: 19001, RelayMetricsPort: 19091, CaddyAdminPort: 12019}}
	if len(networks) == 0 {
		return c, errors.New("未检测到可用 IPv4 网卡，请先为服务器连接局域网，再重新运行首次配置")
	}
	t.m.print("选择连接目标局域网的网卡（请核对地址，避免选到容器或其他虚拟网卡）：")
	for i, network := range networks {
		t.m.print("%d) %s · %s · 网段 %s", i+1, network.name, network.address, network.cidr)
	}
	t.m.print("0) 返回")
	defaultChoice := ""
	if len(networks) == 1 {
		defaultChoice = "1"
	}
	var selected lanNetwork
	for {
		choice, err := t.text("网卡编号", defaultChoice)
		if err != nil {
			return c, err
		}
		if choice == "0" {
			return c, errMenuBack
		}
		index, err := strconv.Atoi(choice)
		if err == nil && index > 0 && index <= len(networks) {
			selected = networks[index-1]
			break
		}
		t.m.print("请按上方地址选择网卡编号。")
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return c, err
	}
	c.Site.ID = "site-" + hex.EncodeToString(id)
	var err error
	if c.Site.Name, err = t.text("站点名称", "NetGo"); err != nil {
		return c, err
	}
	c.LANInterface, c.LocalCIDRs = selected.name, []string{selected.cidr}
	c.Site.Origin = "https://" + net.JoinHostPort(selected.address, "10443")
	c.Site.LocalRelay = selected.address
	c.Site.Candidates = []site.Candidate{{ID: "local", Kind: "direct", Address: net.JoinHostPort(selected.address, "10443")}}
	for {
		t.m.print("\n站点：%s\n管理网址：%s\n局域网：%s · %s\n服务端口：HTTPS %s，Relay %d，STUN %d", c.Site.Name, c.Site.Origin, c.LANInterface, strings.Join(c.LocalCIDRs, ", "), originPort(c.Site.Origin), c.Deployment.LocalRelayPort, c.Deployment.LocalSTUNPort)
		t.m.print("此向导先建立本地站点。内网 IP 无法从公网直接连接；安装完成后可在“设置外网访问”中配对 VPS 或添加公网入口。")
		t.m.print("1) 使用以上设置继续（推荐）  2) 修改域名 / 端口 / 网段  0) 返回")
		choice, err := t.text("请选择", "1")
		if err != nil {
			return c, err
		}
		switch choice {
		case "0":
			return c, errMenuBack
		case "1":
			return c, c.Validate()
		case "2":
			next, err := t.initialAdvanced(c)
			if err != nil {
				return c, err
			}
			c = next
		default:
			t.m.print("请输入菜单中的编号。")
		}
	}
}

func originPort(origin string) string {
	u, _ := url.Parse(origin)
	if u.Port() == "" {
		return "443"
	}
	return u.Port()
}

func (t terminal) initialAdvanced(c site.Config) (site.Config, error) {
	u, _ := url.Parse(c.Site.Origin)
	host, err := t.text("站点证书域名或服务器 IPv4", u.Hostname())
	if err != nil {
		return c, err
	}
	port, err := t.port("控制 HTTPS 端口", deploymentPort(u.Port(), 443))
	if err != nil {
		return c, err
	}
	c.Site = c.Site.ChangeOrigin("https://" + net.JoinHostPort(host, strconv.Itoa(port)))
	relay := c.Site.LocalRelay
	if relay == u.Hostname() {
		relay = host
	}
	if c.Site.LocalRelay, err = t.text("本地 Relay 域名或 IPv4", relay); err != nil {
		return c, err
	}
	if c.Deployment.LocalRelayPort, err = t.port("Relay TCP/UDP 端口", c.Deployment.LocalRelayPort); err != nil {
		return c, err
	}
	if c.Deployment.LocalSTUNPort, err = t.port("STUN UDP 端口", c.Deployment.LocalSTUNPort); err != nil {
		return c, err
	}
	raw, err := t.text("可授权的局域网网段（逗号分隔）", strings.Join(c.LocalCIDRs, ","))
	if err != nil {
		return c, err
	}
	c.LocalCIDRs = nil
	for _, value := range strings.Split(raw, ",") {
		c.LocalCIDRs = append(c.LocalCIDRs, strings.TrimSpace(value))
	}
	return c, c.Validate()
}
