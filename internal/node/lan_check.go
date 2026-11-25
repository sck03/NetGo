package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"netgo/internal/site"
	"slices"
	"strings"
)

// Route samples establish the selected path, not the reachability of a host.
// LAN-only installations need the same interface and forwarding checks.
func (m Manager) checkLAN(c site.Config) error {
	raw, err := m.ip("-j", "address", "show", "dev", c.LANInterface)
	if err != nil {
		return fmt.Errorf("无法读取局域网网卡 %s：%w", c.LANInterface, err)
	}
	var links []struct {
		Name      string   `json:"ifname"`
		Flags     []string `json:"flags"`
		State     string   `json:"operstate"`
		Addresses []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	if err = json.Unmarshal(raw, &links); err != nil {
		return fmt.Errorf("网卡信息无效：%w", err)
	}
	if len(links) != 1 || links[0].Name != c.LANInterface {
		return errors.New("未找到配置的局域网网卡，请检查服务器网卡名称")
	}
	link := links[0]
	if !slices.Contains(link.Flags, "UP") || slices.Contains([]string{"DOWN", "LOWERLAYERDOWN", "NOTPRESENT", "DORMANT"}, link.State) {
		return errors.New("局域网网卡未连接，请检查网线、虚拟网卡或系统网络配置")
	}
	addresses := map[netip.Addr]bool{}
	var labels []string
	for _, value := range link.Addresses {
		ip, err := netip.ParseAddr(value.Local)
		if err == nil && ip.Is4() && ip.IsGlobalUnicast() {
			addresses[ip] = true
			labels = append(labels, ip.String())
		}
	}
	if len(addresses) == 0 {
		return errors.New("局域网网卡没有可用的 IPv4 地址，请先配置服务器地址")
	}
	m.print("局域网网卡已连接；服务器地址：%s", strings.Join(labels, ", "))
	var failures []error
	for _, cidr := range c.LocalCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil || !prefix.Addr().Is4() {
			failures = append(failures, fmt.Errorf("无效的局域网网段：%s", cidr))
			continue
		}
		target := prefix.Masked().Addr()
		if prefix.Bits() < 31 {
			target = target.Next()
		}
		raw, err := m.ip("-j", "route", "get", target.String())
		if err != nil {
			failures = append(failures, fmt.Errorf("网段 %s 无可用路由：%w", cidr, err))
			continue
		}
		var routes []struct {
			Device string `json:"dev"`
			Type   string `json:"type"`
		}
		if err := json.Unmarshal(raw, &routes); err != nil || len(routes) != 1 ||
			(routes[0].Type != "" && routes[0].Type != "unicast" && routes[0].Type != "local") ||
			(routes[0].Device != c.LANInterface && !(routes[0].Type == "local" && addresses[target])) {
			failures = append(failures, fmt.Errorf("网段 %s 的路由未指向网卡 %s，请检查网段和系统路由", cidr, c.LANInterface))
			continue
		}
		m.print("网段 %s：路由抽查 %s 通过（设备连通性需另行验证）。", cidr, target)
	}
	raw, err = m.command("sysctl", "-n", "net.ipv4.ip_forward")
	if err != nil {
		failures = append(failures, err)
	} else if strings.TrimSpace(string(raw)) != "1" {
		failures = append(failures, errors.New("IPv4 转发未启用，VPN 客户端无法访问局域网；请从菜单检查并恢复服务"))
	} else {
		m.print("IPv4 转发已启用。")
	}
	return errors.Join(failures...)
}
