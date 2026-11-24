package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"netgo/internal/gateway"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const routeTable = "19177"
const routeProtocol = "186"

type NetworkSpec struct {
	Owner     string   `json:"owner"`
	Gateway   string   `json:"gateway"`
	LAN       string   `json:"lan"`
	Interface string   `json:"interface"`
	Exclude   []string `json:"exclude"`
}

func networkSpec(c site.Config) (*NetworkSpec, error) {
	if err := c.Bypass.Validate(c.LocalCIDRs, c.OverlayCIDR); err != nil {
		return nil, err
	}
	if !c.Bypass.Enabled {
		return nil, nil
	}
	iface, err := net.InterfaceByName(c.LANInterface)
	if err != nil {
		return nil, err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	gatewayIP, _ := netip.ParseAddr(c.Bypass.Gateway)
	onLink := false
	for _, address := range addresses {
		prefix, e := netip.ParsePrefix(address.String())
		if e != nil {
			continue
		}
		if prefix.Addr() == gatewayIP {
			return nil, errors.New("旁路由不能是服务器自身 IP")
		}
		if prefix.Contains(gatewayIP) {
			onLink = true
		}
	}
	if !onLink {
		return nil, errors.New("旁路由必须位于所选 LAN 网卡的直连网段")
	}
	exclude := map[string]bool{}
	for _, value := range append(append(append([]string{}, gateway.NonPublicIPv4...), c.LocalCIDRs...), c.OverlayCIDR) {
		if value == c.Bypass.FakeIPCIDR {
			continue
		}
		p, e := netip.ParsePrefix(value)
		if e != nil {
			return nil, e
		}
		exclude[p.Masked().String()] = true
	}
	result := &NetworkSpec{Owner: Owner, Gateway: c.Bypass.Gateway, LAN: c.LANInterface, Interface: c.Interface}
	for value := range exclude {
		result.Exclude = append(result.Exclude, value)
	}
	sort.Strings(result.Exclude)
	return result, nil
}
func (m Manager) ip(args ...string) ([]byte, error) {
	return m.command("ip", append([]string{"-4", "-N"}, args...)...)
}
func (m Manager) inspectNetwork(spec *NetworkSpec) error {
	raw, err := m.ip("-j", "rule", "show")
	if err != nil {
		return err
	}
	var rules []map[string]any
	if err = json.Unmarshal(raw, &rules); err != nil {
		return err
	}
	count := 0
	for _, r := range rules {
		if fmt.Sprint(r["priority"]) != routeTable {
			continue
		}
		count++
		if spec == nil || fmt.Sprint(r["table"]) != routeTable || r["iif"] != spec.Interface || fmt.Sprint(r["protocol"]) != routeProtocol || r["src"] != "all" || len(r) != 5 {
			return errors.New("策略路由规则不属于本安装，保留现场")
		}
	}
	if spec != nil && count != 1 {
		return errors.New("受管策略规则缺失")
	}
	raw, err = m.ip("-j", "route", "show", "table", routeTable)
	if err != nil {
		if spec == nil && count == 0 {
			return nil
		}
		return err
	}
	var routes []map[string]any
	if err = json.Unmarshal(raw, &routes); err != nil {
		return err
	}
	if spec == nil {
		if len(routes) != 0 {
			return errors.New("策略路由表已被其他程序使用")
		}
		return nil
	}
	expected := map[string]bool{"default": true}
	for _, cidr := range spec.Exclude {
		expected[cidr] = true
	}
	if len(routes) != len(expected) {
		return errors.New("受管路由表被外部修改")
	}
	for _, r := range routes {
		dst, _ := r["dst"].(string)
		if !expected[dst] || fmt.Sprint(r["protocol"]) != routeProtocol {
			return errors.New("路由归属校验失败")
		}
		delete(expected, dst)
		allowed := map[string]bool{"dst": true, "protocol": true, "flags": true, "type": true, "gateway": true, "dev": true}
		for key := range r {
			if !allowed[key] {
				return errors.New("路由含非受管属性")
			}
		}
		flags, _ := r["flags"].([]any)
		if len(flags) != 0 {
			return errors.New("路由标记已被外部修改")
		}
		if dst == "default" {
			if r["gateway"] != spec.Gateway || r["dev"] != spec.LAN {
				return errors.New("旁路由默认出口被修改")
			}
		} else {
			if fmt.Sprint(r["type"]) != "9" || r["gateway"] != nil || r["dev"] != nil {
				return errors.New("旁路由排除路由被修改")
			}
		}
	}
	return nil
}

type ipChange struct {
	kind string
	args []string
}

func networkChanges(spec *NetworkSpec, action string) []ipChange {
	list := []ipChange{}
	for _, cidr := range spec.Exclude {
		list = append(list, ipChange{"route", []string{action, "throw", cidr, "table", routeTable, "proto", routeProtocol}})
	}
	list = append(list, ipChange{"route", []string{action, "default", "via", spec.Gateway, "dev", spec.LAN, "table", routeTable, "proto", routeProtocol}})
	rule := ipChange{"rule", []string{action, "priority", routeTable, "iif", spec.Interface, "table", routeTable, "protocol", routeProtocol}}
	if action == "add" {
		return append(list, rule)
	}
	reversed := []ipChange{rule}
	for i := len(list) - 1; i >= 0; i-- {
		reversed = append(reversed, list[i])
	}
	return reversed
}
func (m Manager) alterNetwork(spec *NetworkSpec, action string) error {
	completed := []ipChange{}
	for _, change := range networkChanges(spec, action) {
		if _, err := m.ip(append([]string{change.kind}, change.args...)...); err != nil {
			inverse := "add"
			if action == "add" {
				inverse = "del"
			}
			for i := len(completed) - 1; i >= 0; i-- {
				r := completed[i]
				r.args[0] = inverse
				_, _ = m.ip(append([]string{r.kind}, r.args...)...)
			}
			return err
		}
		completed = append(completed, change)
	}
	return nil
}
func (m Manager) ApplyNetwork(c *site.Config) error {
	path := filepath.Join(m.etc(), "bypass-network.json")
	var previous *NetworkSpec
	if _, err := os.Stat(path); err == nil {
		if err = readJSON(path, &previous); err != nil {
			return err
		}
		if previous.Owner != Owner {
			return errors.New("未知旁路由归属记录")
		}
	}
	var next *NetworkSpec
	var err error
	if c != nil {
		next, err = networkSpec(*c)
		if err != nil {
			return err
		}
	}
	if err = m.inspectNetwork(previous); err != nil {
		return err
	}
	if reflect.DeepEqual(previous, next) {
		return nil
	}
	if previous != nil {
		if err = m.alterNetwork(previous, "del"); err != nil {
			return err
		}
	}
	if next != nil {
		err = m.alterNetwork(next, "add")
		if err == nil {
			err = m.json(path, next)
		}
	} else {
		err = os.Remove(path)
		if os.IsNotExist(err) {
			err = nil
		}
	}
	if err != nil {
		if next != nil {
			_ = m.alterNetwork(next, "del")
		}
		if previous != nil {
			if restore := m.alterNetwork(previous, "add"); restore != nil {
				return errors.New("旁路由操作失败且恢复未完成，请保留诊断")
			}
		}
	}
	return err
}
func (m Manager) NetworkRemove() error { return m.ApplyNetwork(nil) }
func (m Manager) Forwarding() error {
	path := filepath.Join(m.etc(), "forwarding.json")
	var state map[string]string
	if err := readJSON(path, &state); os.IsNotExist(err) {
		output, e := m.command("sysctl", "-n", "net.ipv4.ip_forward")
		if e != nil {
			return e
		}
		state = map[string]string{"owner": Owner, "previous": strings.TrimSpace(string(output))}
		if state["previous"] != "0" && state["previous"] != "1" {
			return errors.New("invalid forwarding state")
		}
		if e = m.json(path, state); e != nil {
			return e
		}
	} else if err != nil {
		return err
	}
	_, err := m.command("sysctl", "-w", "net.ipv4.ip_forward=1")
	return err
}
func (m Manager) RestoreForwarding() error {
	var state map[string]string
	err := readJSON(filepath.Join(m.etc(), "forwarding.json"), &state)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if state["owner"] != Owner || (state["previous"] != "0" && state["previous"] != "1") {
		return errors.New("未知转发归属记录")
	}
	_, err = m.command("sysctl", "-w", "net.ipv4.ip_forward="+state["previous"])
	return err
}
func (m Manager) removeTable(table string) error {
	output, err := m.command("nft", "list", "table", "inet", table)
	if err != nil {
		return nil
	}
	owner := gateway.Owner
	if table == "netgo_vps_relay" {
		owner = Owner + " relay redirect"
	}
	if table == "netgo_relay" {
		owner = "NetGo relay redirect v1"
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "table inet "+table+" {" || strings.TrimSpace(lines[1]) != "comment "+strconv.Quote(owner) {
		return errors.New("拒绝删除不属于 NetGo 的防火墙表")
	}
	_, err = m.command("nft", "delete", "table", "inet", table)
	return err
}
