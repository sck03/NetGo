package node

import (
	"context"
	"errors"
	"netgo/internal/site"
	"strings"
)

// Build a draft; validation and successful probing precede any persisted change.
func (t terminal) editBypass(c site.Config, lookup dnsLookup) (site.Config, error) {
	t.m.print("1) 启用/修改旁路由  2) 停用旁路由  0) 返回")
	choice, err := t.text("请选择", "0")
	if err != nil {
		return c, err
	}
	if choice == "0" {
		return c, errMenuBack
	}
	if choice == "2" {
		c.Bypass = site.Bypass{}
		c.Site.InternetDNS = nil
		return c, nil
	}
	if choice != "1" {
		return c, errors.New("无效选项")
	}
	ip, err := t.ipv4("旁路由 IP", c.Bypass.Gateway)
	if err != nil {
		return c, err
	}
	first := ip
	if len(c.Bypass.DNS) > 0 {
		first = c.Bypass.DNS[0]
	}
	dns, err := t.ipv4("首选 DNS IP", first)
	if err != nil {
		return c, err
	}
	fallback := ""
	if len(c.Bypass.DNS) > 1 {
		fallback = strings.Join(c.Bypass.DNS[1:], ",")
	}
	extra, err := t.text("备用 DNS（逗号分隔；输入 - 清空）", fallback)
	if err != nil {
		return c, err
	}
	draft := site.Bypass{Enabled: true, Gateway: ip, DNS: []string{dns}}
	if extra != "" && extra != "-" {
		for _, server := range strings.Split(extra, ",") {
			draft.DNS = append(draft.DNS, strings.TrimSpace(server))
		}
	}
	if err = draft.Validate(c.LocalCIDRs, c.OverlayCIDR); err != nil {
		return c, err
	}
	t.m.print("DNS 模式：1) 自动检测全部 DNS  2) 普通 DNS  3) Fake-IP DNS（Clash/Mihomo）")
	mode, err := t.text("请选择", "1")
	if err != nil {
		return c, err
	}
	switch mode {
	case "1":
		results, e := probeDNS(context.Background(), draft.DNS, lookup)
		if e != nil {
			return c, errors.New(e.Error() + "；配置未保存。请检查 DNS 地址与连接，或确认模式后手动选择。")
		}
		for _, r := range results {
			if r.FakeIP {
				draft.FakeIPCIDR = "198.18.0.0/15"
			}
		}
	case "2":
	case "3":
		draft.FakeIPCIDR = "198.18.0.0/15"
	default:
		return c, errors.New("无效 DNS 模式")
	}
	c.Bypass = draft
	c.Site.InternetDNS = append([]string(nil), draft.DNS...)
	if draft.FakeIPCIDR != "" {
		t.m.print("已启用 Fake-IP DNS 路由与访问规则。")
	} else {
		t.m.print("使用普通 DNS。")
	}
	return c, nil
}
