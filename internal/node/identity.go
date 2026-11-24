package node

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"netgo/internal/site"
	"os"
	"path/filepath"
)

func (m Manager) configureIDP(c site.Config, state SetupState) error {
	var policies []map[string]any
	if err := m.API(c, "/policies", "GET", nil, &policies); err != nil {
		return err
	}
	for _, p := range policies {
		enabled, _ := p["enabled"].(bool)
		if !enabled {
			continue
		}
		if state.Phase != "identity" || p["name"] != "Default" {
			return errors.New("身份服务存在非受管启用策略，请保留现有环境")
		}
		rules := []any{}
		list, _ := p["rules"].([]any)
		for _, value := range list {
			old, ok := value.(map[string]any)
			if !ok {
				return errors.New("invalid upstream policy")
			}
			next := map[string]any{}
			for _, key := range []string{"name", "description", "enabled", "action", "bidirectional", "protocol", "ports", "port_ranges"} {
				if v, ok := old[key]; ok {
					next[key] = v
				}
			}
			for _, key := range []string{"sources", "destinations"} {
				ids := []string{}
				entries, _ := old[key].([]any)
				for _, entry := range entries {
					switch v := entry.(type) {
					case string:
						ids = append(ids, v)
					case map[string]any:
						id, ok := v["id"].(string)
						if !ok {
							return errors.New("invalid group reference")
						}
						ids = append(ids, id)
					default:
						return errors.New("invalid group reference")
					}
				}
				next[key] = ids
			}
			rules = append(rules, next)
		}
		if len(rules) == 0 {
			return errors.New("empty initial policy")
		}
		body := map[string]any{"name": p["name"], "description": p["description"], "enabled": false, "rules": rules}
		if err := m.API(c, "/policies/"+url.PathEscape(fmt.Sprint(p["id"])), "PUT", body, nil); err != nil {
			return err
		}
	}
	var accounts []map[string]any
	if err := m.API(c, "/accounts", "GET", nil, &accounts); err != nil {
		return err
	}
	if len(accounts) != 1 {
		return errors.New("需要单独的 NetGo 身份站点")
	}
	settings, ok := accounts[0]["settings"].(map[string]any)
	if !ok {
		return errors.New("身份服务账户设置缺失")
	}
	settings["network_range"] = c.OverlayCIDR
	settings["ipv6_enabled_groups"] = []string{}
	settings["auto_update_version"] = "disabled"
	return m.API(c, "/accounts/"+url.PathEscape(fmt.Sprint(accounts[0]["id"])), "PUT", map[string]any{"settings": settings}, nil)
}
func (m Manager) enrollRouter(c site.Config, state *SetupState) (result error) {
	var key map[string]any
	body := map[string]any{"name": "netgo-" + c.Site.ID + "-router", "type": "one-off", "expires_in": 3600, "auto_groups": []string{}, "usage_limit": 1, "ephemeral": false}
	if err := m.API(c, "/setup-keys", "POST", body, &key); err != nil {
		return err
	}
	file := m.path("/run/netgo-router/enrollment.key")
	defer func() {
		_ = os.Remove(file)
		if err := m.API(c, "/setup-keys/"+url.PathEscape(fmt.Sprint(key["id"])), "PUT", map[string]any{"auto_groups": []string{}, "revoked": true}, nil); err != nil && result == nil {
			result = err
		}
	}()
	material, ok := key["key"].(string)
	if !ok || material == "" {
		return errors.New("身份服务未返回一次性注册密钥")
	}
	if err := m.write(file, []byte(material+"\n"), 0600); err != nil {
		return err
	}
	if _, err := m.router("up", "--management-url", c.Site.Origin, "--setup-key-file", file, "--hostname", "netgo-"+c.Site.ID+"-router", "--interface-name", c.Interface, "--wireguard-port", "51820", "--disable-dns", "--disable-client-routes", "--disable-ipv6", "--disable-auto-connect=false"); err != nil {
		return err
	}
	var peers []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		IP   string `json:"ip"`
	}
	if err := m.API(c, "/peers", "GET", nil, &peers); err != nil {
		return err
	}
	found := 0
	overlay, _ := netip.ParsePrefix(c.OverlayCIDR)
	for _, p := range peers {
		if p.Name == "netgo-"+c.Site.ID+"-router" {
			found++
			ip, e := netip.ParseAddr(p.IP)
			if e != nil || !overlay.Contains(ip) {
				return errors.New("路由节点地址不属于站点网络")
			}
			state.RouterID = p.ID
			state.RouterIP = p.IP
		}
	}
	if found != 1 {
		return errors.New("路由节点身份不唯一")
	}
	state.Phase = "router"
	return nil
}

var _ = filepath.Separator
