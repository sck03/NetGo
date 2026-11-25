package netbird

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"netgo/internal/model"
	"netgo/internal/site"
	"netgo/internal/store"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type Reconciler struct {
	mu                sync.Mutex
	lastSuccess       time.Time
	lastRevision      uint64
	lastPeers         string
	Management        *Management
	Store             *store.Store
	SiteID, GatewayID string
	LANInterface      string
	Bypass            site.Bypass
}
type ownedPeer struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
}

type object struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Enabled     bool           `json:"enabled"`
	Description string         `json:"description"`
	Raw         map[string]any `json:"-"`
}

func targetsGateway(resource model.Resource, addresses []netip.Addr) bool {
	if !resource.Enabled || resource.Protocol == "internet" {
		return false
	}
	prefix, err := netip.ParsePrefix(resource.CIDR)
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (o *object) UnmarshalJSON(data []byte) error {
	type plain object
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &value.Raw); err != nil {
		return err
	}
	*o = object(value)
	return nil
}

// The upstream read model expands group/peer references and includes read-only
// fields. Compare only the intended write fields, preserving exact membership.
func sameDesired(actual, desired any, key string) bool {
	if key == "sources" || key == "destinations" || key == "groups" || key == "peers" || key == "ports" {
		ids := func(value any) ([]string, bool) {
			if value == nil {
				return []string{}, true
			}
			list, ok := value.([]any)
			if !ok {
				return nil, false
			}
			out := make([]string, 0, len(list))
			for _, item := range list {
				id, ok := item.(string)
				if !ok {
					if expanded, yes := item.(map[string]any); yes {
						id, ok = expanded["id"].(string)
					}
				}
				if !ok || id == "" {
					return nil, false
				}
				out = append(out, id)
			}
			sort.Strings(out)
			return out, true
		}
		a, aok := ids(actual)
		b, bok := ids(desired)
		return aok && bok && reflect.DeepEqual(a, b)
	}
	switch wanted := desired.(type) {
	case map[string]any:
		current, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for field, value := range wanted {
			got, exists := current[field]
			if !exists || !sameDesired(got, value, field) {
				return false
			}
		}
		return true
	case []any:
		current, ok := actual.([]any)
		if !ok || len(current) != len(wanted) {
			return false
		}
		for i := range wanted {
			if !sameDesired(current[i], wanted[i], "") {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(actual, desired)
	}
}

func matchesDesired(o object, data map[string]any) bool {
	encoded, err := json.Marshal(data)
	if err != nil {
		return false
	}
	var wanted any
	if json.Unmarshal(encoded, &wanted) != nil {
		return false
	}
	return sameDesired(o.Raw, wanted, "")
}

func (r *Reconciler) Apply(ctx context.Context) error { return r.applyLatest(ctx, false) }

// Ensure coalesces connection bursts; periodic Apply still detects external drift.
func (r *Reconciler) Ensure(ctx context.Context) error { return r.applyLatest(ctx, true) }
func (r *Reconciler) applyLatest(ctx context.Context, coalesce bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, e := r.Store.PolicyView()
	if e != nil {
		return e
	}
	var peers []ownedPeer
	if _, e = r.Management.Request(ctx, "GET", "/peers", nil, &peers); e != nil {
		return e
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	raw, e := json.Marshal(peers)
	if e != nil {
		return e
	}
	peerHash := model.Hash(string(raw))
	if coalesce && r.lastPeers == peerHash && st.LastError == "" && st.ObservedRevision == st.Revision && r.lastRevision == st.Revision && time.Since(r.lastSuccess) < 2*time.Second {
		return nil
	}
	e = r.apply(ctx, st, peers)
	message := ""
	if e != nil {
		message = "network reconciliation failed"
	}
	saveErr := r.Store.Observe(st.Revision, message)
	if e == nil {
		e = saveErr
	}
	if e == nil {
		r.lastRevision = st.Revision
		r.lastPeers = peerHash
		r.lastSuccess = time.Now()
	}
	return e
}
func (r *Reconciler) apply(ctx context.Context, st model.State, peers []ownedPeer) error {
	m := r.Management
	prefix := "netgo:" + r.SiteID + ":"
	var policies []object
	if _, e := m.Request(ctx, "GET", "/policies", nil, &policies); e != nil {
		return e
	}
	for _, p := range policies {
		if p.Enabled && !strings.HasPrefix(p.Name, prefix) {
			return errors.New("unmanaged enabled NetBird policy: review/disable it before applying NetGo")
		}
	}
	var groups, networks []object
	if _, e := m.Request(ctx, "GET", "/groups", nil, &groups); e != nil {
		return e
	}
	if _, e := m.Request(ctx, "GET", "/networks", nil, &networks); e != nil {
		return e
	}
	upsert := func(path, name string, data map[string]any, objects []object) (string, error) {
		data["name"] = name
		for _, o := range objects {
			if o.Name == name {
				if matchesDesired(o, data) {
					return o.ID, nil
				}
				var out object
				_, e := m.Request(ctx, "PUT", path+"/"+o.ID, data, &out)
				return o.ID, e
			}
		}
		var out object
		_, e := m.Request(ctx, "POST", path, data, &out)
		return out.ID, e
	}
	networkID, e := upsert("/networks", prefix+"lan", map[string]any{"description": "Managed by NetGo"}, networks)
	if e != nil {
		return e
	}
	if r.GatewayID == "" {
		return errors.New("gateway peer ID required")
	}
	localAddresses := []netip.Addr{}
	if r.LANInterface != "" {
		iface, err := net.InterfaceByName(r.LANInterface)
		if err != nil {
			return err
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return err
		}
		for _, address := range addresses {
			if prefix, e := netip.ParsePrefix(address.String()); e == nil && prefix.Addr().Is4() {
				localAddresses = append(localAddresses, prefix.Addr())
			}
		}
	}
	gatewayGroup, e := upsert("/groups", prefix+"gateway", map[string]any{"peers": []string{r.GatewayID}}, groups)
	if e != nil {
		return e
	}
	desiredGroups := map[string]bool{prefix + "gateway": true}
	var routers []struct {
		ID         string `json:"id"`
		Peer       string `json:"peer"`
		Masquerade bool   `json:"masquerade"`
		Enabled    bool   `json:"enabled"`
		Metric     int    `json:"metric"`
	}
	if _, e = m.Request(ctx, "GET", "/networks/"+networkID+"/routers", nil, &routers); e != nil {
		return e
	}
	router := map[string]any{"peer": r.GatewayID, "masquerade": true, "metric": 999, "enabled": true}
	if len(routers) == 0 {
		_, e = m.Request(ctx, "POST", "/networks/"+networkID+"/routers", router, nil)
	} else if len(routers) == 1 && routers[0].Peer == r.GatewayID {
		if !routers[0].Enabled || !routers[0].Masquerade || routers[0].Metric != 999 {
			_, e = m.Request(ctx, "PUT", "/networks/"+networkID+"/routers/"+routers[0].ID, router, nil)
		}
	} else {
		return errors.New("unexpected routers in managed network")
	}
	if e != nil {
		return e
	}
	var existingResources []object
	if _, e = m.Request(ctx, "GET", "/networks/"+networkID+"/resources", nil, &existingResources); e != nil {
		return e
	}
	resourceGroups := map[string]string{}
	desiredResources := map[string]bool{}
	ids := make([]string, 0, len(st.Resources))
	for id := range st.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := st.Resources[id]
		name := prefix + "resource:" + id
		desiredGroups[name] = true
		gid, e := upsert("/groups", name, map[string]any{"peers": []string{}}, groups)
		if e != nil {
			return e
		}
		resourceGroups[id] = gid
		_, e = upsert("/networks/"+networkID+"/resources", name, map[string]any{"address": v.CIDR, "enabled": v.Enabled, "groups": []string{gid}, "description": "Managed by NetGo"}, existingResources)
		if e != nil {
			return e
		}
		desiredResources[name] = true
	}
	desiredPolicies := map[string]bool{}
	dnsGroups := []string{}
	// Register source groups from management-authoritative ownership, not submitted IDs.
	for _, u := range st.Users {
		sourcePeers := []string{}
		if !u.Disabled {
			for _, p := range peers {
				if p.UserID == u.ID && p.ID != r.GatewayID {
					sourcePeers = append(sourcePeers, p.ID)
				}
			}
		}
		name := prefix + "user:" + model.Hash(u.ID)[:16]
		desiredGroups[name] = true
		gid, e := upsert("/groups", name, map[string]any{"peers": sourcePeers}, groups)
		if e != nil {
			return e
		}
		for _, rid := range u.Resources {
			v, ok := st.Resources[rid]
			if !ok {
				continue
			}
			if v.Protocol == "internet" && v.Enabled && !u.Disabled {
				dnsGroups = append(dnsGroups, gid)
			}
			name := prefix + "policy:" + model.Hash(u.ID + ":" + rid)[:24]
			ports := []string{}
			for _, p := range v.Ports {
				ports = append(ports, fmt.Sprint(p))
			}
			protocol := v.Protocol
			if protocol == "internet" {
				protocol = "all"
			}
			rule := map[string]any{"name": name, "enabled": true, "action": "accept", "bidirectional": false, "protocol": protocol, "sources": []string{gid}, "destinations": []string{resourceGroups[rid]}}
			if len(ports) > 0 {
				rule["ports"] = ports
			}
			_, e = upsert("/policies", name, map[string]any{"description": "Managed by NetGo", "enabled": v.Enabled && !u.Disabled, "rules": []any{rule}}, policies)
			if e != nil {
				return e
			}
			desiredPolicies[name] = true
			if targetsGateway(v, localAddresses) {
				hostName := prefix + "host:" + model.Hash(u.ID + ":" + rid)[:24]
				hostRule := map[string]any{"name": hostName, "enabled": true, "action": "accept", "bidirectional": false, "protocol": protocol, "sources": []string{gid}, "destinations": []string{gatewayGroup}}
				if len(ports) > 0 {
					hostRule["ports"] = ports
				}
				if _, e = upsert("/policies", hostName, map[string]any{"description": "NetGo gateway service; exact address and live lease enforced by local gate", "enabled": !u.Disabled, "rules": []any{hostRule}}, policies); e != nil {
					return e
				}
				desiredPolicies[hostName] = true
			}
		}
	}
	if e := r.reconcileDNS(ctx, prefix, dnsGroups); e != nil {
		return e
	}
	for _, route := range model.RemoteRoutes(st) {
		name := prefix + "remote:" + model.Hash(route.ID)[:24]
		desiredGroups[name+":controller"] = true
		desiredGroups[name+":host"] = true
		source, e := upsert("/groups", name+":controller", map[string]any{"peers": []string{route.ControllerPeer}}, groups)
		if e != nil {
			return e
		}
		destination, e := upsert("/groups", name+":host", map[string]any{"peers": []string{route.HostPeer}}, groups)
		if e != nil {
			return e
		}
		rule := map[string]any{"name": name, "enabled": true, "action": "accept", "bidirectional": false, "protocol": "tcp", "sources": []string{source}, "destinations": []string{destination}, "ports": []string{fmt.Sprint(route.Port)}}
		if _, e = upsert("/policies", name, map[string]any{"description": "NetGo remote session: one controller, one host, one TLS port", "enabled": true, "rules": []any{rule}}, policies); e != nil {
			return e
		}
		desiredPolicies[name] = true
	}
	for _, p := range policies {
		if strings.HasPrefix(p.Name, prefix) && !desiredPolicies[p.Name] {
			if _, e = m.Request(ctx, "DELETE", "/policies/"+p.ID, nil, nil); e != nil {
				return e
			}
		}
	}
	for _, v := range existingResources {
		if strings.HasPrefix(v.Name, prefix) && !desiredResources[v.Name] {
			if _, e = m.Request(ctx, "DELETE", "/networks/"+networkID+"/resources/"+v.ID, nil, nil); e != nil {
				return e
			}
		}
	}
	for _, g := range groups {
		if strings.HasPrefix(g.Name, prefix) && !desiredGroups[g.Name] {
			if _, e = m.Request(ctx, "DELETE", "/groups/"+g.ID, nil, nil); e != nil {
				return e
			}
		}
	}
	return nil
}
