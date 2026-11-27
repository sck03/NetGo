package model

import "slices"

func (r Resource) Normalized() Resource {
	r.Ports = slices.Clone(r.Ports)
	slices.Sort(r.Ports)
	r.Ports = slices.Compact(r.Ports)
	return r
}

// SameAccess deliberately excludes presentation metadata.
func (r Resource) SameAccess(other Resource) bool {
	r, other = r.Normalized(), other.Normalized()
	return r.ID == other.ID && r.CIDR == other.CIDR && r.Protocol == other.Protocol &&
		r.Enabled == other.Enabled && slices.Equal(r.Ports, other.Ports)
}
