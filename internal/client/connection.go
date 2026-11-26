package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"netgo/internal/controlpath"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"strings"
	"time"
)

func (c *Controller) run(ctx context.Context, gen uint64, p site.Profile, alias string, password *string, replace, internet bool) (result error) {
	defer func() { *password = "" }()
	selector, e := controlpath.New(p)
	if e != nil {
		return e
	}
	defer func() { selector.CloseIdleConnections() }()
	if _, e = selector.Select(ctx); e != nil {
		return e
	}
	c.set(gen, func(s *Status) {
		s.Control = selector.Current().Kind
		s.ControlCandidate = selector.Current().ID
		s.State = "authenticating"
	})
	scope := site.AccountScope(p.ID, alias)
	seed, e := c.Vault.Read(scope)
	if e != nil {
		if !os.IsNotExist(e) {
			return e
		}
		seed = make([]byte, ed25519.SeedSize)
		if _, e = rand.Read(seed); e != nil {
			return e
		}
		if e = c.Vault.Write(scope, seed); e != nil {
			return e
		}
	}
	defer func() { clear(seed) }()
	if len(seed) != ed25519.SeedSize {
		return errors.New("invalid protected device key")
	}
	pk := ed25519.NewKeyFromSeed(seed)
	defer func() { clear(pk) }()
	engineProfile := func() site.Profile { v := p; v.ID = scope; return v }
	loginCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	wg, e := c.Engine.Prepare(loginCtx, engineProfile())
	if e != nil {
		return e
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := c.Engine.Down(cleanup); err != nil {
			c.mu.Lock()
			c.cleanupFailed = true
			c.mu.Unlock()
			result = errors.New("engine_cleanup_failed")
		}
	}()
	remote := &Remote{HTTP: selector.Client(), Origin: p.Origin, Private: pk, Now: selector.Now}
	verifier := model.ID()
	begin := identity.Begin{SiteID: p.ID, PublicKey: base64.RawURLEncoding.EncodeToString(pk.Public().(ed25519.PublicKey)), WireGuardKey: wg, Challenge: model.Challenge(verifier)}
	var transaction identity.Transaction
	if e = remote.Call(loginCtx, "POST", "/netgo/v2/auth/transactions", begin, &transaction, ""); e != nil {
		return e
	}
	transaction.Begin = begin
	proof := identity.Proof(transaction)
	sign := func(s string) string { return base64.RawURLEncoding.EncodeToString(ed25519.Sign(pk, []byte(s))) }
	if e = remote.Call(loginCtx, "POST", "/netgo/v2/auth/transactions/"+transaction.ID+"/complete", map[string]string{"Alias": alias, "Password": *password, "Signature": sign(proof)}, nil, ""); e != nil {
		return e
	}
	var auth identity.Result
	if e = remote.Call(loginCtx, "POST", "/netgo/v2/auth/transactions/"+transaction.ID+"/exchange", map[string]string{"Verifier": verifier, "Signature": sign(proof + "\nexchange\n" + verifier)}, &auth, ""); e != nil {
		return e
	}
	remote.Token = auth.Token
	// Only the authenticated site may update discovery addresses. Probe before
	// committing so a broken published candidate set cannot strand the profile.
	p, selector, e = c.synchronizeProfile(loginCtx, remote, p, selector)
	if e != nil {
		return e
	}
	if e = c.saveVerifiedLogin(gen, p, alias, *password, internet); e != nil {
		return e
	}
	*password = ""
	remote.HTTP = selector.Client()
	remote.Now = selector.Now
	if engine, ok := c.Engine.(InternetEngine); ok {
		if e = engine.ConfigureInternet(loginCtx, internet, p.InternetDNS); e != nil {
			return e
		}
	} else if internet {
		return errors.New("internet_not_supported")
	}
	c.set(gen, func(s *Status) { s.Control = selector.Current().Kind; s.ControlCandidate = selector.Current().ID })
	if e = c.Engine.Login(loginCtx, engineProfile(), auth.IDToken); e != nil {
		return e
	}
	if e = c.Engine.Up(loginCtx, engineProfile()); e != nil {
		return e
	}
	if e = c.Engine.WaitConnected(loginCtx, engineProfile()); e != nil {
		return e
	}
	var peer struct{ ID string }
	if e = remote.Call(loginCtx, "GET", "/netgo/v2/peer", nil, &peer, ""); e != nil {
		return e
	}
	var lease model.Lease
	if e = remote.Call(loginCtx, "POST", "/netgo/v2/sessions", map[string]any{"peer_id": peer.ID, "generation": gen, "replace": replace}, &lease, model.ID()); e != nil {
		return e
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = remote.Call(cleanup, "DELETE", "/netgo/v2/sessions/"+lease.ID, nil, nil, "")
	}()
	applyRoutes := func(parent context.Context) error {
		return applySelectedRoutes(parent, c.Engine, lease.Resources, internet)
	}
	if e = applyRoutes(loginCtx); e != nil {
		return e
	}
	c.set(gen, func(s *Status) { s.State = "negotiating"; s.Lease = lease })
	remoteConnection := c.attachRemote(ctx, p.ID, scope, auth, pk, remote)
	defer c.detachRemote(remoteConnection)
	restoreEngine := func(token string) error {
		recovery, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := c.Engine.Login(recovery, engineProfile(), token); err != nil {
			return err
		}
		if err := c.Engine.Up(recovery, engineProfile()); err != nil {
			return err
		}
		return applyRoutes(recovery)
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	lastRenew := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !selector.Now().Before(lease.ExpiresAt) {
				return errors.New("lease_expired")
			}
			check, done := context.WithTimeout(ctx, 8*time.Second)
			e = selector.Recover(check)
			done()
			c.set(gen, func(s *Status) { s.Control = selector.Current().Kind; s.ControlCandidate = selector.Current().ID })
			if e != nil {
				c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = e.Error() })
				continue
			}
			if auth.ExpiresAt.Sub(selector.Now()) < 2*time.Minute && auth.RefreshToken != "" {
				var renewed identity.Result
				if renewed, e = remote.Refresh(ctx, auth.RefreshToken); e != nil {
					if terminalControlError(e) {
						return e
					}
					c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = "authentication_refresh_failed" })
					continue
				}
				auth = renewed
				if e = restoreEngine(renewed.IDToken); e != nil {
					c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = e.Error() })
					continue
				}
			}
			if time.Since(lastRenew) >= 30*time.Second || lease.ExpiresAt.Sub(selector.Now()) < 40*time.Second {
				e = remote.Call(ctx, "POST", "/netgo/v2/sessions/"+lease.ID+"/renew", map[string]any{"Generation": gen}, &lease, "")
				if e != nil {
					if terminalControlError(e) {
						return e
					}
					c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = e.Error() })
					continue
				}
				lastRenew = time.Now()
				if e = applyRoutes(ctx); e != nil {
					return e
				}
			}
			stateCtx, done := context.WithTimeout(ctx, 5*time.Second)
			ds, de := c.Engine.Status(stateCtx, engineProfile())
			done()
			if de != nil || !strings.EqualFold(ds.State, "connected") {
				_ = restoreEngine(auth.IDToken)
				c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = "engine_recovering" })
				continue
			}
			c.set(gen, func(s *Status) {
				s.Lease = lease
				s.Data = ds
				s.Error = ""
				// Remote-only accounts establish their peer path after choosing a host.
				if len(lease.Resources) > 0 && ds.Path == "negotiating" {
					s.State = "negotiating"
				} else {
					s.State = "connected"
				}
			})
		}
	}
}
