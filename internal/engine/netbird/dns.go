package netbird

import "context"

func (r *Reconciler) reconcileDNS(ctx context.Context, prefix string, groups []string) error {
	var existing []object
	m := r.Management
	if _, err := m.Request(ctx, "GET", "/dns/nameservers", nil, &existing); err != nil {
		return err
	}
	name := prefix + "internet-dns"
	enabled := r.Bypass.Enabled && len(groups) > 0
	data := map[string]any{"name": name, "description": "NetGo optional Internet DNS", "enabled": true, "primary": true, "domains": []string{}, "search_domains_enabled": false, "groups": groups}
	servers := []any{}
	for _, ip := range r.Bypass.DNS {
		servers = append(servers, map[string]any{"ip": ip, "port": 53, "ns_type": "udp"})
	}
	data["nameservers"] = servers
	for _, current := range existing {
		if current.Name != name {
			continue
		}
		if !enabled {
			_, err := m.Request(ctx, "DELETE", "/dns/nameservers/"+current.ID, nil, nil)
			return err
		}
		if matchesDesired(current, data) {
			return nil
		}
		_, err := m.Request(ctx, "PUT", "/dns/nameservers/"+current.ID, data, nil)
		return err
	}
	if enabled {
		_, err := m.Request(ctx, "POST", "/dns/nameservers", data, nil)
		return err
	}
	return nil
}
