//go:build linux

package node

import (
	"encoding/json"
	"netgo/internal/site"
	"os"
	"strings"
	"testing"
)

func TestKernelBypassRoutingOwnershipAndRestoration(t *testing.T) {
	if os.Getenv("NETGO_PRIVILEGED_TESTS") != "1" {
		t.Skip("requires explicitly isolated root network namespace")
	}
	current, e := os.Readlink("/proc/self/ns/net")
	if e != nil {
		t.Fatal(e)
	}
	host, e := os.Readlink("/proc/1/ns/net")
	if e != nil || current == host || os.Geteuid() != 0 {
		t.Fatal("refusing network test in host namespace")
	}
	m := Manager{Role: "server", Root: physicalTemp(t)}
	for _, pair := range [][2]string{{"lan0", "192.168.18.2/24"}, {"wt0", "100.96.0.1/16"}} {
		for _, args := range [][]string{{"link", "add", pair[0], "type", "dummy"}, {"link", "set", pair[0], "up"}, {"address", "add", pair[1], "dev", pair[0]}} {
			if _, e = m.command("ip", args...); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = m.ip("route", "add", "default", "via", "192.168.18.254", "dev", "lan0"); e != nil {
		t.Fatal(e)
	}
	baseline, e := m.ip("-j", "route", "show", "table", "main")
	if e != nil {
		t.Fatal(e)
	}
	c := site.Config{Interface: "wt0", LANInterface: "lan0", OverlayCIDR: "100.96.0.0/16", LocalCIDRs: []string{"192.168.18.0/24"}, Bypass: site.Bypass{Enabled: true, Gateway: "192.168.18.1", DNS: []string{"192.168.18.1"}}}
	if e = m.ApplyNetwork(&c); e != nil {
		t.Fatal(e)
	}
	if e = m.ApplyNetwork(&c); e != nil {
		t.Fatal("repeat apply", e)
	}
	raw, e := m.ip("-j", "route", "get", "1.1.1.1", "from", "100.96.0.2", "iif", "wt0")
	if e != nil {
		t.Fatal(e)
	}
	var route []map[string]any
	json.Unmarshal(raw, &route)
	if len(route) != 1 || route[0]["gateway"] != "192.168.18.1" {
		t.Fatal("Internet route did not use selected bypass", string(raw))
	}
	raw, e = m.ip("-j", "route", "get", "192.168.18.20", "from", "100.96.0.2", "iif", "wt0")
	if e != nil {
		t.Fatal(e)
	}
	route = nil
	json.Unmarshal(raw, &route)
	if len(route) != 1 || route[0]["gateway"] != nil {
		t.Fatal("LAN route was sent through bypass", string(raw))
	}
	if _, e = m.command("ip", "link", "del", "wt0"); e != nil {
		t.Fatal(e)
	}
	if e = m.ApplyNetwork(&c); e != nil {
		t.Fatal("detached rule on daemon restart", e)
	}
	if e = m.NetworkRemove(); e != nil {
		t.Fatal("remove detached rule", e)
	}
	if e = m.ApplyNetwork(&c); e != nil {
		t.Fatal("apply before VPN interface exists", e)
	}
	for _, args := range [][]string{{"link", "add", "wt0", "type", "dummy"}, {"link", "set", "wt0", "up"}, {"address", "add", "100.96.0.1/16", "dev", "wt0"}} {
		if _, e = m.command("ip", args...); e != nil {
			t.Fatal(e)
		}
	}
	if e = m.ApplyNetwork(&c); e != nil {
		t.Fatal("reattached VPN rule", e)
	}
	raw, e = m.ip("-j", "route", "get", "1.1.1.1", "from", "100.96.0.2", "iif", "wt0")
	if e != nil {
		t.Fatal(e)
	}
	route = nil
	json.Unmarshal(raw, &route)
	if len(route) != 1 || route[0]["gateway"] != "192.168.18.1" {
		t.Fatal("reattached rule lost bypass", string(raw))
	}
	if _, e = m.ip("route", "add", "198.20.0.0/16", "dev", "lan0", "table", routeTable, "proto", "187"); e != nil {
		t.Fatal(e)
	}
	if e = m.NetworkRemove(); e == nil {
		t.Fatal("foreign route was overwritten")
	}
	if _, e = m.ip("route", "del", "198.20.0.0/16", "dev", "lan0", "table", routeTable, "proto", "187"); e != nil {
		t.Fatal(e)
	}
	if e = m.NetworkRemove(); e != nil {
		t.Fatal(e)
	}
	if e = m.NetworkRemove(); e != nil {
		t.Fatal("repeated removal", e)
	}
	after, e := m.ip("-j", "route", "show", "table", "main")
	if e != nil || string(after) != string(baseline) {
		t.Fatal("host default routes changed")
	}
	if _, e = m.ip("rule", "add", "priority", routeTable, "iif", "wt0", "table", routeTable, "protocol", "187"); e != nil {
		t.Fatal(e)
	}
	if e = m.ApplyNetwork(&c); e == nil {
		t.Fatal("foreign rule overwritten")
	}
	after, e = m.ip("-j", "rule", "show")
	if e != nil || !strings.Contains(string(after), "187") {
		t.Fatal("foreign rule was removed")
	}
}
