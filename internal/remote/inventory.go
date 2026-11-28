package remote

import (
	"context"
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
	NextAfter      string                `json:"next_after"`
	Hosts          []HostSummary         `json:"hosts"`
	Grants         []GrantSummary        `json:"grants"`
	Sessions       []model.RemoteSession `json:"sessions"`
	NextGeneration uint64                `json:"next_generation"`
}

func (m *Manager) List(ctx context.Context, c model.Credential, after string) (Inventory, error) {
	page, err := m.Store.RemoteInventory(ctx, c, after)
	if err != nil {
		return Inventory{}, err
	}
	out, err := m.inventory(c, page.State, page.Generation)
	if err != nil {
		return out, err
	}
	hosts := out.Hosts[:0]
	for _, h := range out.Hosts {
		if page.Hosts[h.ID] {
			hosts = append(hosts, h)
		}
	}
	out.Hosts = hosts
	grants := out.Grants[:0]
	for _, g := range out.Grants {
		if page.Hosts[g.HostID] && g.UserID == c.UserID {
			grants = append(grants, g)
		}
	}
	out.Grants = grants
	sessions := out.Sessions[:0]
	for _, session := range out.Sessions {
		if session.ControllerID == c.DeviceID || session.HostID == c.DeviceID {
			sessions = append(sessions, session)
		}
	}
	out.Sessions = sessions
	out.NextAfter = page.NextAfter
	return out, nil
}

type InventoryPage struct {
	Hosts     []HostSummary         `json:"hosts"`
	Sessions  []model.RemoteSession `json:"sessions"`
	NextAfter string                `json:"next_after"`
	Users     map[string]string     `json:"users"`
}

func (m *Manager) AdminList(ctx context.Context, c model.Credential, after, search string) (InventoryPage, error) {
	st, next, err := m.Store.AdminRemotePage(ctx, c, after, search)
	if err != nil {
		return InventoryPage{}, err
	}
	if st.Users[c.UserID].Role != "admin" {
		return InventoryPage{}, model.ErrForbidden
	}
	inventory, err := m.inventory(c, st, 0)
	labels := make(map[string]string, len(st.Users))
	for id, u := range st.Users {
		labels[id] = u.Alias
	}
	return InventoryPage{Hosts: inventory.Hosts, Sessions: inventory.Sessions, NextAfter: next, Users: labels}, err
}

func (m *Manager) inventory(c model.Credential, state model.State, lastGeneration uint64) (Inventory, error) {
	out := Inventory{Hosts: []HostSummary{}, Grants: []GrantSummary{}, Sessions: []model.RemoteSession{}}
	e := func(st *model.State) error {
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
