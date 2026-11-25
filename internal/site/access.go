package site

import (
	"errors"
	"net/netip"
	"netgo/internal/model"
)

// Access describes only the settings an administrator needs to create usable
// resources. Never serialize Config: it also contains private service settings.
type Access struct {
	LANInterface    string   `json:"lan_interface"`
	LocalCIDRs      []string `json:"local_cidrs"`
	InternetEnabled bool     `json:"internet_enabled"`
	Gateway         string   `json:"gateway,omitempty"`
	DNS             []string `json:"dns"`
	FakeIPCIDR      string   `json:"fake_ip_cidr,omitempty"`
}

func (c Config) Access() Access {
	return Access{c.LANInterface, append([]string{}, c.LocalCIDRs...), c.Bypass.Enabled,
		c.Bypass.Gateway, append([]string{}, c.Bypass.DNS...), c.Bypass.FakeIPCIDR}
}

func (c Config) ValidateResource(resource model.Resource) error {
	if err := ValidateResource(resource, c.OverlayCIDR); err != nil {
		return err
	}
	if !resource.Enabled {
		return nil
	}
	if resource.Protocol == "internet" {
		if !c.Bypass.Enabled {
			return errors.New("请先在服务器菜单中配置互联网出口，再启用旁路由资源")
		}
		return nil
	}
	target, _ := netip.ParsePrefix(resource.CIDR)
	for _, value := range c.LocalCIDRs {
		network, err := netip.ParsePrefix(value)
		if err == nil && network.Addr().Is4() && network.Bits() <= target.Bits() && network.Contains(target.Addr()) {
			return nil
		}
	}
	return errors.New("资源地址必须位于服务器配置的局域网网段内；请修改目标地址或先在服务器菜单中添加网段")
}
