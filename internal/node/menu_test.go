package node

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"netgo/internal/site"
	"strings"
	"testing"
)

func testTerminal(input string) (terminal, *bytes.Buffer) {
	output := &bytes.Buffer{}
	return terminal{Manager{Out: output}, bufio.NewReader(strings.NewReader(input))}, output
}

func TestSetupRecommendationBuildsUniqueValidatedSite(t *testing.T) {
	networks := []lanNetwork{{"ens32", "192.168.18.220", "192.168.18.0/23"}}
	term, out := testTerminal("\n办公室\n\n")
	config, err := term.initialConfigWithNetworks(networks)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.Site.Origin != "https://192.168.18.220:10443" || config.Site.Name != "办公室" || config.LANInterface != "ens32" || config.LocalCIDRs[0] != "192.168.18.0/23" {
		t.Fatalf("unexpected detected config: %+v", config)
	}
	if config.Site.Candidates[0].Address != "192.168.18.220:10443" || config.Site.LocalRelay != "192.168.18.220" {
		t.Fatal("local addresses diverge")
	}
	if !strings.Contains(out.String(), "内网 IP 无法从公网直接连接") {
		t.Fatal("missing reachability explanation")
	}
	other, _ := testTerminal("\n\n\n")
	again, err := other.initialConfigWithNetworks(networks)
	if err != nil || !site.ValidID(again.Site.ID) || again.Site.ID == config.Site.ID {
		t.Fatal("site identity was reused", err)
	}
}

func TestSetupMultiInterfaceDoesNotGuessNetwork(t *testing.T) {
	term, out := testTerminal("\n9\n2\n分公司\n1\n")
	c, err := term.initialConfigWithNetworks([]lanNetwork{{"eth0", "10.1.1.2", "10.1.1.0/24"}, {"eth1", "192.168.18.2", "192.168.18.0/24"}})
	if err != nil || c.LANInterface != "eth1" {
		t.Fatal(c, err)
	}
	if strings.Count(out.String(), "请按上方地址选择网卡编号") != 2 {
		t.Fatal("ambiguous default or invalid choice accepted")
	}
	term, _ = testTerminal("0\n")
	if _, err = term.initialConfigWithNetworks([]lanNetwork{{"eth0", "10.1.1.2", "10.1.1.0/24"}}); !errors.Is(err, errMenuBack) {
		t.Fatal(err)
	}
	term, _ = testTerminal("")
	if _, err = term.initialConfigWithNetworks(nil); err == nil {
		t.Fatal("accepted no network")
	}
}

func TestSetupAdvancedKeepsSeparateIdentityAndValidatesNetworks(t *testing.T) {
	c := configFixture(t)
	term, _ := testTerminal("new.example\n11443\n\n19443\n4478\n192.168.60.0/24\n")
	next, err := term.initialAdvanced(c)
	if err != nil {
		t.Fatal(err)
	}
	if next.Site.Origin != "https://new.example:11443" || next.Deployment.LocalSTUNPort != 4478 || next.LocalCIDRs[0] != "192.168.60.0/24" || next.Site.ID != c.Site.ID {
		t.Fatal(next)
	}
	term, _ = testTerminal("\n\n\n\n\n100.96.0.0/16\n")
	if _, err = term.initialAdvanced(c); err == nil {
		t.Fatal("overlapping overlay accepted")
	}
}

func TestVPSRecommendedSettingsAndCustomEdits(t *testing.T) {
	original := site.Auxiliary{ID: "vps1", Mode: "diagnostic", TokenFile: "/etc/netgo/native.secret", ControlPort: 24443, TunnelPort: 27000}
	term, _ := testTerminal("\n203.0.113.20\n")
	a, err := term.auxiliarySettings(original, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Host != a.TunnelHost || a.Relay != "rels://203.0.113.20:28443" || a.STUN != "stun:203.0.113.20:3478" || a.ControlPort != 24443 || a.TunnelPort != 27000 {
		t.Fatal(a)
	}
	a.ControlPort, a.TunnelPort, a.Relay, a.STUN = 25443, 28000, "rels://203.0.113.20:29443", "stun:203.0.113.20:4478"
	term, _ = testTerminal(strings.Repeat("\n", 8))
	next, err := term.auxiliarySettings(a, false)
	if err != nil || next != a {
		t.Fatal("editing reset custom ports", next, err)
	}
	for _, host := range []string{"192.168.18.220", "100.64.0.1", "127.0.0.1", "0.0.0.0", "localhost"} {
		term, _ = testTerminal("1\n" + host + "\n")
		if _, err := term.auxiliarySettings(original, true); err == nil {
			t.Fatal("private VPS accepted as recommended public entry", host)
		}
	}
}

func TestMenuReturnAndEndOfInputDoNotLoop(t *testing.T) {
	term, out := testTerminal("bad\n1\n0\n")
	called := 0
	err := term.menu("Test", menuItem{"1", "back", func() error { called++; return errMenuBack }})
	if err != nil || called != 1 || strings.Contains(out.String(), "操作未完成") {
		t.Fatal(called, err, out.String())
	}
	term, _ = testTerminal("")
	if err = term.serverMenu(); err != nil {
		t.Fatal(err)
	}
	term, _ = testTerminal("1\n")
	if err = term.menu("Test", menuItem{"1", "EOF", func() error { return io.EOF }}); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointStatusDistinguishesReachabilityFromConfiguration(t *testing.T) {
	for address, want := range map[string]string{"192.168.18.220:10443": "内网地址", "100.64.0.1:443": "内网地址", "[::ffff:192.168.1.1]:443": "内网地址", "[::1]:443": "仅本机", "localhost:443": "仅本机", "0.0.0.0:443": "非公网入口", "203.0.113.1:443": "尚需验证", "site.example:443": "需验证外网解析"} {
		if got := endpointScope(address); !strings.Contains(got, want) {
			t.Fatal(address, got)
		}
	}
}
