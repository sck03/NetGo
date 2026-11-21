package identity

import (
	"context"
	"errors"
	"netgo/internal/model"
	"strings"
	"time"
)

type Refresher interface {
	Refresh(context.Context, string) (Tokens, error)
}

func (n *Native) Refresh(ctx context.Context, c model.Credential, oldToken, refresh string) (Result, error) {
	p, ok := n.Provider.(Refresher)
	if !ok || refresh == "" || len(refresh) > 16384 {
		return Result{}, errors.New("refresh unavailable")
	}
	if e := n.Provider.UserActive(ctx, c.UserID); e != nil {
		return Result{}, model.ErrForbidden
	}
	t, e := p.Refresh(ctx, refresh)
	if e != nil {
		return Result{}, e
	}
	result := Result{Token: model.ID(), IDToken: t.IDToken, RefreshToken: t.RefreshToken, ExpiresAt: t.Expiry, DeviceID: c.DeviceID}
	e = n.Store.Update(func(st *model.State) error {
		existing, ok := st.Credentials[model.Hash(oldToken)]
		if !ok || existing.DeviceID != c.DeviceID || existing.UserID != c.UserID || !time.Now().Before(existing.ExpiresAt) {
			return model.ErrUnauthorized
		}
		d := st.Devices[c.DeviceID]
		parts := strings.Split(t.Nonce, ":")
		u := st.Users[c.UserID]
		if t.Subject != u.ID || !u.Active(time.Now()) || len(parts) != 4 || parts[1] != d.PublicKey || parts[2] != d.WireGuardKey {
			return model.ErrForbidden
		}
		if !u.ExpiresAt.IsZero() && u.ExpiresAt.Before(result.ExpiresAt) {
			result.ExpiresAt = u.ExpiresAt
		}
		delete(st.Credentials, model.Hash(oldToken))
		st.Credentials[model.Hash(result.Token)] = model.Credential{UserID: u.ID, DeviceID: d.ID, ExpiresAt: result.ExpiresAt}
		result.User = u
		st.Record(u.ID, "auth.refresh", d.ID)
		return nil
	})
	return result, e
}
