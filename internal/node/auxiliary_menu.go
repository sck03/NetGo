package node

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"netgo/internal/site"
	"strconv"
	"strings"
)

func (t terminal) auxiliarySettings(current site.Auxiliary, create bool) (site.Auxiliary, error) {
	t.m.print("1) 推荐设置：只填写一个 VPS 公网 IP / 域名\n2) 自定义地址和端口\n3) 三个域名共用 443\n0) 返回")
	defaultChoice := "2"
	if create {
		defaultChoice = "1"
	}
	choice, err := t.text("请选择", defaultChoice)
	if err != nil {
		return current, err
	}
	if choice == "0" {
		return current, errMenuBack
	}
	if choice != "1" && choice != "2" && choice != "3" {
		return current, errors.New("无效选项")
	}
	a := current
	if a.Host, err = t.text("VPS 公网 IP / 域名（不含协议和端口）", a.Host); err != nil {
		return current, err
	}
	if choice == "1" {
		if strings.EqualFold(strings.TrimSuffix(a.Host, "."), "localhost") {
			return current, errors.New("localhost 只能访问本机，请填写 VPS 公网地址")
		}
		if ip, e := netip.ParseAddr(a.Host); e == nil && (ip.IsPrivate() || !ip.IsGlobalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip)) {
			return current, errors.New("此地址无法从公网直接访问，请使用 VPS 的公网地址；专用内网连接可在自定义设置中配置")
		}
		a.Mode, a.TunnelHost = "diagnostic", a.Host
		a.ControlPort, a.TunnelPort = 24443, 27000
		a.Relay = "rels://" + net.JoinHostPort(a.Host, "28443")
		a.STUN = "stun:" + net.JoinHostPort(a.Host, "3478")
	} else {
		relay, relayPort, stunPort := a.Host, 28443, 3478
		if u, e := url.Parse(a.Relay); e == nil && u.Hostname() != "" {
			relay, relayPort = u.Hostname(), deploymentPort(u.Port(), relayPort)
		}
		if _, port, e := net.SplitHostPort(strings.TrimPrefix(a.STUN, "stun:")); e == nil {
			stunPort = deploymentPort(port, stunPort)
		}
		if choice == "3" {
			t.m.print("请填写分别用于控制、Relay 和隧道的三个域名；域名解析和 VPS 的 443 端口需事先就绪。")
		}
		if relay, err = t.text("VPS Relay 域名 / IP", relay); err != nil {
			return current, err
		}
		tunnel := a.TunnelHost
		if tunnel == "" || tunnel == current.Host {
			tunnel = a.Host
		}
		if a.TunnelHost, err = t.text("VPS 隧道域名 / IP", tunnel); err != nil {
			return current, err
		}
		a.Mode = "diagnostic"
		if choice == "3" {
			a.Mode, a.ControlPort, a.TunnelPort, relayPort = "443", 443, 443, 443
		} else {
			if a.ControlPort, err = t.port("控制入口 TCP 端口", a.PublicControlPort()); err != nil {
				return current, err
			}
			if a.TunnelPort, err = t.port("反向隧道 TCP 端口", a.PublicTunnelPort()); err != nil {
				return current, err
			}
			if relayPort, err = t.port("Relay TCP/UDP 端口", relayPort); err != nil {
				return current, err
			}
		}
		if stunPort, err = t.port("STUN UDP 端口", stunPort); err != nil {
			return current, err
		}
		a.Relay = "rels://" + net.JoinHostPort(relay, strconv.Itoa(relayPort))
		a.STUN = "stun:" + net.JoinHostPort(relay, strconv.Itoa(stunPort))
	}
	if err = a.Validate(); err != nil {
		return current, err
	}
	t.m.print("VPS 控制入口：%s\n反向隧道：%s\n中继：%s\nSTUN：%s", net.JoinHostPort(a.Host, strconv.Itoa(a.PublicControlPort())), net.JoinHostPort(a.TunnelHost, strconv.Itoa(a.PublicTunnelPort())), a.Relay, a.STUN)
	t.m.print("请在 VPS / 云防火墙放行相应端口。接下来导出配对包，再在 VPS 菜单中导入；仅保存地址不会建立连接。")
	return a, nil
}

func (t terminal) vpsAction(action string) func() error {
	return func() error { return t.vpsOperation(action) }
}

func (t terminal) vpsMenu() error {
	return t.menu("NetGo VPS 辅助节点 · ngv",
		menuItem{"1", "导入配对包 / 启动节点", t.vpsAction("import")},
		menuItem{"2", "查看连接与运行状态", t.vpsAction("status")},
		menuItem{"3", "检查并恢复连接", t.vpsAction("repair")},
		menuItem{"4", "高级设置", func() error {
			return t.menu("VPS 高级设置",
				menuItem{"1", "修改服务端口", t.vpsAction("ports")},
				menuItem{"2", "更新证书（导入 LAN 新配对包）", t.vpsAction("import")})
		}},
		menuItem{"9", "卸载（保留配置）", t.vpsAction("uninstall")})
}
