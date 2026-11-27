package session

import (
	"context"
	"encoding/json"
	"netgo/internal/gateway"
	"netgo/internal/model"
	"reflect"
	"sort"
)

// Called only under Manager.mu. Capacity is updated from the serialized delta,
// avoiding a full policy serialization for every unchanged lease renewal.
func (m *Manager) refreshLease(ctx context.Context, next model.Lease) (bool, error) {
	renewer, ok := m.Gate.(gateway.Renewer)
	if !ok || m.lastApply.IsZero() {
		return false, nil
	}
	i := sort.Search(len(m.applied), func(i int) bool { return m.applied[i].ID >= next.ID })
	if i == len(m.applied) || m.applied[i].ID != next.ID {
		return false, nil
	}
	old := m.applied[i]
	identity := next
	identity.ExpiresAt = old.ExpiresAt
	if !reflect.DeepEqual(old, identity) {
		return false, nil
	}
	before, err := json.Marshal(old)
	if err != nil {
		return false, err
	}
	after, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	size := m.appliedBytes + len(after) - len(before)
	if size > gateway.MaxPolicyBytes {
		return false, gateway.ErrCapacity
	}
	handled, err := renewer.Renew(ctx, next)
	if err == nil && handled {
		m.applied[i], m.appliedBytes = next, size
		m.lastApply = m.now()
	}
	return handled, err
}

func (m *Manager) rememberApplied(leases []model.Lease) {
	m.applied, m.lastApply = leases, m.now()
	data, _ := json.Marshal(leases) // Apply already validated these JSON values.
	m.appliedBytes = len(data)
}
