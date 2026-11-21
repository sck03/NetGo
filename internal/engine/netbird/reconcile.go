package netbird

import (
	"context"
	"errors"
	"fmt"
	"netgo/internal/model"
	"netgo/internal/store"
	"sort"
	"strings"
	"sync"
)

type Reconciler struct {
	mu                sync.Mutex
	Management        *Management
	Store             *store.Store
	SiteID, GatewayID string
}
type object struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

func (r *Reconciler) Apply(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, e := r.Store.View()
	if e != nil {
		return e
	}
	e = r.apply(ctx, st)
	_ = r.Store.Update(func(s *model.State) error {
		if e != nil {
			s.LastError = "network reconciliation failed"
		} else {
			s.LastError = ""
			s.ObservedRevision = st.Revision
		}
		return nil
	})
	return e
}
func (r *Reconciler) apply(ctx context.Context, st model.State) error {
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
	var routers []struct {
		ID   string `json:"id"`
		Peer string `json:"peer"`
	}
	if _, e = m.Request(ctx, "GET", "/networks/"+networkID+"/routers", nil, &routers); e != nil {
		return e
	}
	router := map[string]any{"peer": r.GatewayID, "masquerade": true, "metric": 999, "enabled": true}
	if len(routers) == 0 {
		_, e = m.Request(ctx, "POST", "/networks/"+networkID+"/routers", router, nil)
	} else if len(routers) == 1 && routers[0].Peer == r.GatewayID {
		_, e = m.Request(ctx, "PUT", "/networks/"+networkID+"/routers/"+routers[0].ID, router, nil)
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
	// Register source groups from management-authoritative ownership, not submitted IDs.
	var peers []struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if _, e = m.Request(ctx, "GET", "/peers", nil, &peers); e != nil {
		return e
	}
	for _, u := range st.Users {
		sourcePeers := []string{}
		if !u.Disabled {
			for _, p := range peers {
				if p.UserID == u.ID && p.ID != r.GatewayID {
					sourcePeers = append(sourcePeers, p.ID)
				}
			}
		}
		gid, e := upsert("/groups", prefix+"user:"+model.Hash(u.ID)[:16], map[string]any{"peers": sourcePeers}, groups)
		if e != nil {
			return e
		}
		for _, rid := range u.Resources {
			v, ok := st.Resources[rid]
			if !ok {
				continue
			}
			name := prefix + "policy:" + model.Hash(u.ID + ":" + rid)[:24]
			ports := []string{}
			for _, p := range v.Ports {
				ports = append(ports, fmt.Sprint(p))
			}
			rule := map[string]any{"name": name, "enabled": true, "action": "accept", "bidirectional": false, "protocol": v.Protocol, "sources": []string{gid}, "destinations": []string{resourceGroups[rid]}}
			if len(ports) > 0 {
				rule["ports"] = ports
			}
			_, e = upsert("/policies", name, map[string]any{"description": "Managed by NetGo", "enabled": v.Enabled && !u.Disabled, "rules": []any{rule}}, policies)
			if e != nil {
				return e
			}
			desiredPolicies[name] = true
		}
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
	return nil
}
