package node

import (
	"bytes"
	"context"
	"errors"
	"netgo/internal/site"
	"strings"
	"testing"
)

func TestCheckNetworkChecksLANWithoutBypass(t *testing.T) {
	for _, tc := range []struct {
		name, link, route, forwarding, want string
		commandError                        bool
	}{
		{name: "ready"},
		{name: "carrier down", link: `[{"ifname":"ens32","flags":["UP"],"operstate":"DOWN"}]`, want: "未连接"},
		{name: "admin down", link: `[{"ifname":"ens32","flags":[],"operstate":"UNKNOWN"}]`, want: "未连接"},
		{name: "missing interface", link: `[]`, want: "未找到"},
		{name: "IPv6 only", link: `[{"ifname":"ens32","flags":["UP"],"addr_info":[{"local":"fe80::1"}]}]`, want: "IPv4 地址"},
		{name: "wrong route", route: `[{"dev":"eth9"}]`, want: "未指向"},
		{name: "blackhole", route: `[{"dev":"ens32","type":"blackhole"}]`, want: "未指向"},
		{name: "empty route", route: `[]`, want: "未指向"},
		{name: "invalid route", route: `invalid`, want: "未指向"},
		{name: "forwarding disabled", forwarding: "0", want: "IPv4 转发未启用"},
		{name: "kernel read failure", commandError: true, want: "执行失败"},
		{name: "server address route", route: `[{"dev":"lo","type":"local"}]`},
		{name: "foreign local route", route: `[{"dev":"lo","type":"local"}]`, link: `[{"ifname":"ens32","flags":["UP"],"addr_info":[{"local":"192.168.18.220"}]}]`, want: "未指向"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := configFixture(t)
			c.Bypass = site.Bypass{}
			c.Site.InternetDNS = nil
			c.LANInterface = "ens32"
			c.LocalCIDRs = []string{"192.168.18.0/24"}
			var out bytes.Buffer
			m := Manager{Root: t.TempDir(), Out: &out}
			m.Run = func(_ context.Context, name string, args []string, _ []byte, _ []string) ([]byte, error) {
				switch name + " " + strings.Join(args, " ") {
				case "ip -4 -N -j address show dev ens32":
					if tc.link != "" {
						return []byte(tc.link), nil
					}
					return []byte(`[{"ifname":"ens32","flags":["UP","LOWER_UP"],"operstate":"UP","addr_info":[{"local":"192.168.18.1"}]}]`), nil
				case "ip -4 -N -j route get 192.168.18.1":
					if tc.route != "" {
						return []byte(tc.route), nil
					}
					return []byte(`[{"dev":"ens32","prefsrc":"192.168.18.1"}]`), nil
				case "sysctl -n net.ipv4.ip_forward":
					if tc.commandError {
						return nil, errors.New("failed")
					}
					if tc.forwarding != "" {
						return []byte(tc.forwarding), nil
					}
					return []byte("1\n"), nil
				default:
					t.Fatalf("unexpected/mutating command: %s %v", name, args)
					return nil, errors.New("unexpected command")
				}
			}
			err := m.CheckNetwork(c)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "IPv4 转发已启用") || !strings.Contains(out.String(), "路由抽查") {
					t.Fatal(out.String())
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
