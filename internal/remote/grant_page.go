package remote

import (
	"context"
	"netgo/internal/model"
	"sort"
)

type GrantPage struct {
	Grants    []GrantSummary    `json:"grants"`
	Users     map[string]string `json:"users"`
	NextAfter string            `json:"next_after"`
}

func (m *Manager) AdminGrants(ctx context.Context, c model.Credential, host, user, after string) (GrantPage, error) {
	st, next, err := m.Store.AdminGrantPage(ctx, c, host, user, after)
	if err != nil {
		return GrantPage{}, err
	}
	now := m.now()
	if err = current(&st, c, now); err != nil {
		return GrantPage{}, err
	}
	if st.Users[c.UserID].Role != "admin" {
		return GrantPage{}, model.ErrForbidden
	}
	out := GrantPage{Grants: []GrantSummary{}, Users: map[string]string{}, NextAfter: next}
	for _, g := range st.RemoteGrants {
		out.Grants = append(out.Grants, GrantSummary{RemoteGrant: g, Active: grantActive(g, now)})
		out.Users[g.UserID] = st.Users[g.UserID].Alias
	}
	sort.Slice(out.Grants, func(i, j int) bool { return out.Grants[i].ID < out.Grants[j].ID })
	return out, nil
}
