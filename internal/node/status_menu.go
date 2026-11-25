package node

import (
	"net"
	"net/netip"
	"netgo/internal/site"
	"path/filepath"
	"strings"
)

func endpointScope(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "地址格式错误"
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return "仅本机"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "域名，需验证外网解析与可达性"
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return "仅本机"
	}
	if ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return "内网地址，无法从公网直接访问"
	}
	if !ip.IsGlobalUnicast() {
		return "非公网入口"
	}
	return "IP 入口，尚需验证公网路由与端口"
}

func (t terminal) serverStatus(c site.Config) error {
	t.m.print("站点：%s\n管理网址 / 证书身份：%s\n客户端站点文件：%s", c.Site.Name, c.Site.Origin, filepath.Join(t.m.etc(), "client-site.json"))
	t.m.print("连接入口：")
	for _, candidate := range c.Site.Candidates {
		label := "直达"
		if candidate.Kind == "auxiliary" {
			label = "VPS 备用（需完成节点配对）"
		}
		t.m.print("  %s：%s · %s", label, candidate.Address, endpointScope(candidate.Address))
	}
	if len(c.Auxiliaries) == 0 {
		t.m.print("未配对 VPS。只有内网入口时，请通过“设置外网访问”添加可达的公网入口或 VPS。")
	}
	t.m.print("程序自动选择连接入口；配置成功或服务运行不代表已通过外网实测。账号、资源和远程桌面权限请在管理网页中设置。")
	if c.Bypass.Enabled {
		t.m.print("互联网出口：已配置旁路由 %s", c.Bypass.Gateway)
	} else {
		t.m.print("互联网出口：关闭（仅访问获授权的资源）")
	}
	for _, unit := range wantedUnits(c) {
		status := "未运行"
		if t.m.active(unit) {
			status = "运行中"
		}
		t.m.print("%s：%s", unit, status)
	}
	return nil
}
