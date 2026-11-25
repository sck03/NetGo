package node

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/site"
	"strings"
)

// CheckNetwork only inspects the LAN node. It never changes routes, firewall,
// DNS, or services and does not imply that a remote client has authorization.
func (m Manager) CheckNetwork(c site.Config) error {
	m.print("局域网网卡：%s；资源网段：%s", c.LANInterface, strings.Join(c.LocalCIDRs, ", "))
	if !c.Bypass.Enabled {
		m.print("互联网出口未启用。局域网资源仍需在管理网页中单独授权。")
		return nil
	}
	m.print("旁路由：%s；DNS：%s", c.Bypass.Gateway, strings.Join(c.Bypass.DNS, ", "))
	var failures []error
	spec, err := networkSpec(c)
	if err != nil {
		failures = append(failures, err)
	} else if err = m.inspectNetwork(spec); err != nil {
		failures = append(failures, fmt.Errorf("旁路由策略路由未就绪：%w；可从菜单检查并恢复服务", err))
	} else {
		m.print("旁路由位于所选网卡直连网段，受管策略路由正常。")
	}
	results, err := probeDNS(context.Background(), c.Bypass.DNS, lookupDNS)
	if err != nil {
		failures = append(failures, err)
	}
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		mode := "普通 IPv4"
		if r.FakeIP {
			mode = "Fake-IP"
		}
		m.print("DNS %s：查询成功（%s）", r.Server, mode)
		if r.FakeIP && c.Bypass.FakeIPCIDR == "" {
			failures = append(failures, fmt.Errorf("DNS %s 返回 Fake-IP，请在互联网出口设置中启用自动检测或 Fake-IP 模式", r.Server))
		}
	}
	m.print("客户端访问还需：账号获授局域网资源；使用旁路由时另授互联网资源，并在客户端打开互联网模式。")
	m.print("本检查验证服务器配置与 DNS；实际外网、VPS 和客户端访问以连接实测为准。")
	return errors.Join(failures...)
}
