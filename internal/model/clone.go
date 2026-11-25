package model

import (
	"maps"
	"slices"
)

// Clone isolates transaction callbacks without serializing the entire database.
func (s State) Clone() State {
	c := s
	c.Users = maps.Clone(s.Users)
	for id, u := range c.Users {
		u.Resources = slices.Clone(u.Resources)
		c.Users[id] = u
	}
	c.Resources = maps.Clone(s.Resources)
	for id, r := range c.Resources {
		r.Ports = slices.Clone(r.Ports)
		c.Resources[id] = r
	}
	c.Devices = maps.Clone(s.Devices)
	c.Leases = maps.Clone(s.Leases)
	for id, l := range c.Leases {
		l.Resources = slices.Clone(l.Resources)
		for i := range l.Resources {
			l.Resources[i].Ports = slices.Clone(l.Resources[i].Ports)
		}
		c.Leases[id] = l
	}
	c.Credentials = maps.Clone(s.Credentials)
	c.Operations = maps.Clone(s.Operations)
	c.Hosts = maps.Clone(s.Hosts)
	c.RemoteGrants = maps.Clone(s.RemoteGrants)
	c.RemoteSessions = maps.Clone(s.RemoteSessions)
	c.RemoteOperations = maps.Clone(s.RemoteOperations)
	c.Audit = slices.Clone(s.Audit)
	return c
}

// SessionLimit is shared by VPN and remote access, including bootstrap accounts.
func (u User) SessionLimit() int {
	if u.MaxSessions == 0 {
		return 1
	}
	return u.MaxSessions
}
