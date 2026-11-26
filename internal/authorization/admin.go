// Package authorization carries the authenticated administrator to the commit
// boundary. Background jobs use an unmarked context and record a system actor.
package authorization

import (
	"context"
	"netgo/internal/model"
	"time"
)

type adminKey struct{}

func WithAdmin(ctx context.Context, c model.Credential) context.Context {
	return context.WithValue(ctx, adminKey{}, c)
}
func Actor(ctx context.Context) string {
	if c, ok := ctx.Value(adminKey{}).(model.Credential); ok {
		return c.UserID
	}
	return "system"
}
func CheckAdmin(ctx context.Context, st *model.State) error {
	c, marked := ctx.Value(adminKey{}).(model.Credential)
	if !marked {
		return nil
	}
	now := time.Now()
	saved, ok := st.Credentials[c.TokenHash]
	u, userOK := st.Users[c.UserID]
	d, deviceOK := st.Devices[c.DeviceID]
	if !ok || !userOK || !deviceOK || saved.UserID != c.UserID || saved.DeviceID != c.DeviceID ||
		!saved.ExpiresAt.Equal(c.ExpiresAt) || !now.Before(c.ExpiresAt) || !u.Active(now) || d.DeletePending || d.UserID != u.ID {
		return model.ErrUnauthorized
	}
	if u.Role != "admin" {
		return model.ErrForbidden
	}
	return nil
}
