package model

// PruneDependencies applies domain ownership invariants in the same transaction
// as the mutation. Storage adapters persist the result without owning policy.
func PruneDependencies(before, st *State) {
	// Resource ownership is resolved atomically with scoped lease revocation.
	for id, u := range st.Users {
		resources := make([]string, 0, len(u.Resources))
		for _, rid := range u.Resources {
			_, was := before.Resources[rid]
			_, exists := st.Resources[rid]
			if !was || exists {
				resources = append(resources, rid)
			}
		}
		if len(resources) != len(u.Resources) {
			u.Resources = resources
			st.Users[id] = u
		}
	}
	// A device/user deletion revokes dependent credentials and leases in this
	// transaction, even when a caller only removes the owning entity.
	for id, d := range st.Devices {
		_, existed := before.Users[d.UserID]
		if _, ok := st.Users[d.UserID]; !ok && existed {
			delete(st.Devices, id)
		}
	}
	for key, c := range st.Credentials {
		d, ok := st.Devices[c.DeviceID]
		_, existed := before.Devices[c.DeviceID]
		if !ok && existed || ok && (d.DeletePending || d.UserID != c.UserID) {
			delete(st.Credentials, key)
		}
	}
	for key, l := range st.Leases {
		d, ok := st.Devices[l.DeviceID]
		_, existed := before.Devices[l.DeviceID]
		if !ok && existed || ok && (d.DeletePending || d.UserID != l.UserID) {
			delete(st.Leases, key)
		}
	}
	for id := range st.Hosts {
		_, existed := before.Devices[id]
		if d, ok := st.Devices[id]; !ok && existed || ok && d.DeletePending {
			delete(st.Hosts, id)
		}
	}
	for id, g := range st.RemoteGrants {
		_, u := st.Users[g.UserID]
		_, h := st.Hosts[g.HostID]
		_, oldUser := before.Users[g.UserID]
		_, oldHost := before.Hosts[g.HostID]
		if !u && oldUser || !h && oldHost {
			delete(st.RemoteGrants, id)
		}
	}
	for id, r := range st.RemoteSessions {
		_, u := st.Users[r.UserID]
		_, h := st.Hosts[r.HostID]
		d, c := st.Devices[r.ControllerID]
		_, oldUser := before.Users[r.UserID]
		_, oldHost := before.Hosts[r.HostID]
		_, oldController := before.Devices[r.ControllerID]
		if !u && oldUser || !h && oldHost || !c && oldController || c && d.DeletePending {
			delete(st.RemoteSessions, id)
		}
	}
}
