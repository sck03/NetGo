package client

import (
	"context"
	"encoding/json"
	"netgo/internal/controlpath"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"path/filepath"
)

func (c *Controller) synchronizeProfile(ctx context.Context, remote *Remote, current site.Profile, selector *controlpath.Selector) (site.Profile, *controlpath.Selector, error) {
	var next site.Profile
	if err := remote.Call(ctx, "GET", "/netgo/v2/site", nil, &next, ""); err != nil {
		return current, selector, err
	}
	if err := site.CheckProfileUpdate(current, next); err != nil {
		return current, selector, err
	}
	next.Name = current.Name
	if next.ConfigRevision == current.ConfigRevision && site.SameProfileContent(current, next) {
		return current, selector, nil
	}
	updated, err := controlpath.New(next)
	if err != nil {
		return current, selector, err
	}
	accepted := false
	defer func() {
		if !accepted {
			updated.CloseIdleConnections()
		}
	}()
	if _, err = updated.Select(ctx); err != nil {
		return current, selector, err
	}
	if err = ctx.Err(); err != nil {
		return current, selector, err
	}
	err = c.saveProfile(next)
	if err != nil {
		return current, selector, err
	}
	selector.CloseIdleConnections()
	accepted = true
	return next, updated, nil
}

func (c *Controller) saveProfile(p site.Profile) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err == nil {
		err = securefile.Write(filepath.Join(c.Root, p.ID+".json"), b, 0600)
	}
	if err == nil {
		c.mu.Lock()
		c.profilesRevision++
		c.mu.Unlock()
	}
	return err
}
