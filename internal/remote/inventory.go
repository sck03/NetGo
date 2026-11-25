package remote

import (
	"math"
	"netgo/internal/model"
	"sort"
	"time"
)

// HostSummary is a live view of a registered host. Availability and effective
// permissions use server time and are never persisted by clients.
type HostSummary struct {
	model.Host
	Status                string `json:"status"`
	AvailableCapabilities uint32 `json:"available_capabilities"`
}

type GrantSummary struct {
	model.RemoteGrant
	Active bool `json:"active"`
}

func hostStatus(st *model.State, h model.Host, now time.Time) string {
	if !h.Enabled {
		return "disabled"
	}
	if now.Before(h.LastSeen) || now.Sub(h.LastSeen) >= HeartbeatTimeout {
		return "offline"
	}
	d, ok := st.Devices[h.ID]
	u, exists := st.Users[d.UserID]
	if !ok || !exists || !u.Active(now) {
		return "vpn_offline"
	}
	if _, ok := activeLease(st, h.ID, now); !ok {
		return "vpn_offline"
	}
	return "ready"
}

func grantActive(g model.RemoteGrant, now time.Time) bool {
	return g.ExpiresAt.IsZero() || now.Before(g.ExpiresAt)
}

type Inventory struct {
	Hosts          []HostSummary         `json:"hosts"`
	Grants         []GrantSummary        `json:"grants"`
	Sessions       []model.RemoteSession `json:"sessions"`
	NextGeneration uint64                `json:"next_generation"`
}

func (m *Manager) List(c model.Credential) (Inventory, error) {
	out := Inventory{Hosts: []HostSummary{}, Grants: []GrantSummary{}, Sessions: []model.RemoteSession{}}
	state, lastGeneration, e := m.Store.RemoteInventory(c.DeviceID)
	if e != nil {
		return out, e
	}
	e = func(st *model.State) error {
		now := m.now()
		if e := current(st, c, now); e != nil {
			return e
		}
		m.sweep(st, now)
		admin := st.Users[c.UserID].Role == "admin"
		allowed := map[string]bool{c.DeviceID: true}
		for _, g := range st.RemoteGrants {
			if admin || g.UserID == c.UserID {
				out.Grants = append(out.Grants, GrantSummary{RemoteGrant: g, Active: grantActive(g, now)})
				allowed[g.HostID] = true
			}
		}
		busy := map[string]bool{}
		for _, r := range st.RemoteSessions {
			if r.State != "closed" {
				busy[r.HostID] = true
			}
		}
		for _, h := range st.Hosts {
			if admin || allowed[h.ID] {
				summary := HostSummary{Host: h, Status: hostStatus(st, h, now)}
				if summary.Status == "ready" && busy[h.ID] {
					summary.Status = "busy"
				}
				g, granted := st.RemoteGrants[model.Hash(c.UserID+":"+h.ID)]
				if granted && grantActive(g, now) && h.ID != c.DeviceID {
					summary.AvailableCapabilities = g.Capabilities & h.Capabilities
				}
				out.Hosts = append(out.Hosts, summary)
			}
		}
		for _, r := range st.RemoteSessions {
			if r.State != "closed" && (admin || r.ControllerID == c.DeviceID || r.HostID == c.DeviceID) {
				r.ControllerAuth = ""
				out.Sessions = append(out.Sessions, r)
			}
		}
		if lastGeneration < math.MaxInt64 {
			out.NextGeneration = lastGeneration + 1
		}
		return nil
	}(&state)
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].ID < out.Hosts[j].ID })
	sort.Slice(out.Grants, func(i, j int) bool { return out.Grants[i].ID < out.Grants[j].ID })
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].ID < out.Sessions[j].ID })
	return out, e
}
