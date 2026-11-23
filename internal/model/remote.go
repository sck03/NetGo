package model

import (
	"sort"
	"time"
)

// Hosts are explicitly enabled by their authenticated device. Boot changes fence
// all tickets from the preceding process; the address is obtained from NetBird.
type Host struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Platform       string    `json:"platform"`
	Boot           string    `json:"boot"`
	Enabled        bool      `json:"enabled"`
	Capabilities   uint32    `json:"capabilities"`
	RequireConsent bool      `json:"require_consent"`
	Port           uint16    `json:"port"`
	Revision       uint64    `json:"revision"`
	LastSeen       time.Time `json:"last_seen"`
}

type RemoteGrant struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	HostID       string    `json:"host_id"`
	Capabilities uint32    `json:"capabilities"`
	ExpiresAt    time.Time `json:"expires_at"`
	Revision     uint64    `json:"revision"`
}

// Ticket bytes are reproducible from immutable claims. Never persist private
// keys, media, clipboard contents or a controller's possession proof here.
type RemoteSession struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	ControllerID    string    `json:"controller_id"`
	HostID          string    `json:"host_id"`
	HostBoot        string    `json:"host_boot"`
	GrantID         string    `json:"grant_id"`
	Binding         string    `json:"binding"`
	Generation      uint64    `json:"generation"`
	PolicyRevision  uint64    `json:"policy_revision"`
	Sequence        uint64    `json:"sequence"`
	Capabilities    uint32    `json:"capabilities"`
	RequireConsent  bool      `json:"require_consent"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	State           string    `json:"state"`
	ControllerLease string    `json:"controller_lease"`
	HostLease       string    `json:"host_lease"`
	ControllerAuth  string    `json:"controller_auth"`
}

type RemoteOperation struct {
	Fingerprint string    `json:"fingerprint"`
	SessionID   string    `json:"session_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// RemoteRoutes is the minimal overlay policy projection. Lease sequence/time
// changes do not cause policy churn; identity removal closes the route at once.
type RemoteRoute struct {
	ID, ControllerPeer, HostPeer string
	Port                         uint16
}

func RemoteRoutes(st State) []RemoteRoute {
	out := []RemoteRoute{}
	for id, r := range st.RemoteSessions {
		h, hok := st.Hosts[r.HostID]
		c, cok := st.Devices[r.ControllerID]
		d, dok := st.Devices[r.HostID]
		g, gok := st.RemoteGrants[r.GrantID]
		auth, aok := st.Credentials[r.ControllerAuth]
		cl, clok := st.Leases[r.ControllerLease]
		hl, hlok := st.Leases[r.HostLease]
		if r.State == "closed" || !hok || !h.Enabled || h.Boot != r.HostBoot || !cok || !dok || !gok || g.Revision != r.PolicyRevision || !aok || auth.DeviceID != c.ID || st.Users[c.UserID].Disabled || st.Users[d.UserID].Disabled || !clok || !hlok || cl.DeviceID != c.ID || hl.DeviceID != d.ID || c.PeerID == "" || d.PeerID == "" || c.PeerID != cl.PeerID || d.PeerID != hl.PeerID {
			continue
		}
		out = append(out, RemoteRoute{ID: id, ControllerPeer: c.PeerID, HostPeer: d.PeerID, Port: h.Port})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
