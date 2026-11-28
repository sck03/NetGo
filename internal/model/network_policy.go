package model

import "slices"

// NetworkPolicyChanged compares only fields consumed by policy reconciliation
// and access lifetime. Editing a label or retry metadata must not resync NetBird.
// Account edit/auth revisions remain independent optimistic-concurrency fences.
func NetworkPolicyChanged(before, after *State) bool {
	if len(before.Users) != len(after.Users) || len(before.Resources) != len(after.Resources) {
		return true
	}
	for id, a := range before.Users {
		b, ok := after.Users[id]
		if !ok || a.ID != b.ID || a.Disabled != b.Disabled || a.DeletePending != b.DeletePending ||
			!a.ExpiresAt.Equal(b.ExpiresAt) || !slices.Equal(a.Resources, b.Resources) {
			return true
		}
	}
	for id, a := range before.Resources {
		b, ok := after.Resources[id]
		if !ok || a.ID != b.ID || a.CIDR != b.CIDR || a.Protocol != b.Protocol ||
			a.Enabled != b.Enabled || !slices.Equal(a.Ports, b.Ports) {
			return true
		}
	}
	return !slices.Equal(RemoteRoutes(*before), RemoteRoutes(*after))
}
