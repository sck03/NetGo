package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"netgo/internal/controlpath"
	"netgo/internal/engine/netbird"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/protocol"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Engine interface {
	Prepare(context.Context, site.Profile) (string, error)
	Login(context.Context, site.Profile, string) error
	Up(context.Context, site.Profile) error
	Down(context.Context) error
	Status(context.Context, site.Profile) (netbird.DataStatus, error)
	Routes(context.Context, []model.Resource) error
	WaitConnected(context.Context, site.Profile) error
}
type DeviceVault interface {
	Read(string) ([]byte, error)
	Write(string, []byte) error
}
type Status struct {
	Generation       uint64             `json:"generation"`
	State            string             `json:"state"`
	Error            string             `json:"error"`
	SiteID           string             `json:"site_id"`
	Control          string             `json:"control"`
	ControlCandidate string             `json:"control_candidate"`
	Data             netbird.DataStatus `json:"data"`
	Lease            model.Lease        `json:"lease"`
	Internet         bool               `json:"internet"`
}
type Controller struct {
	mu            sync.Mutex
	op            sync.Mutex
	Root          string
	Vault         DeviceVault
	Engine        Engine
	status        Status
	cancel        context.CancelFunc
	done          chan struct{}
	cleanupFailed bool
	remote        *remoteConnection
}
type Remote struct {
	HTTP          *http.Client
	Origin, Token string
	Private       ed25519.PrivateKey
}

func (r *Remote) Call(ctx context.Context, method, path string, in, out any, key string) error {
	var b []byte
	if in != nil {
		b, _ = json.Marshal(in)
	}
	req, e := http.NewRequestWithContext(ctx, method, r.Origin+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(protocol.Header, strconv.Itoa(model.Protocol))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if r.Token != "" {
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonce := model.ID()
		message := identity.SignatureMessage(method, path, stamp, nonce, b, r.Token)
		req.Header.Set("Authorization", "Bearer "+r.Token)
		req.Header.Set("X-NetGo-Time", stamp)
		req.Header.Set("X-NetGo-Nonce", nonce)
		req.Header.Set("X-NetGo-Signature", base64.RawURLEncoding.EncodeToString(ed25519.Sign(r.Private, []byte(message))))
	}
	res, e := r.HTTP.Do(req)
	if e != nil {
		return errors.New("control_unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&v)
		if v.Error == "" {
			v.Error = fmt.Sprintf("HTTP_%d", res.StatusCode)
		}
		return errors.New(v.Error)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
	}
	return nil
}
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	if s.State == "" {
		s.State = "disconnected"
	}
	return s
}
func (c *Controller) Import(p site.Profile) error {
	return c.importProfile(p, false)
}

// ImportTrusted is only used after an explicit local user confirms a new host/CA.
// Automatic updates continue to use CheckProfileUpdate.
func (c *Controller) ImportTrusted(p site.Profile) error { return c.importProfile(p, true) }
func (c *Controller) importProfile(p site.Profile, replaceTrust bool) error {
	c.op.Lock()
	defer c.op.Unlock()
	if e := p.Validate(); e != nil {
		return e
	}
	if c.Status().State != "disconnected" && c.Status().State != "failed" {
		return errors.New("disconnect before importing site")
	}
	c.mu.Lock()
	failedCleanup := c.cleanupFailed
	c.mu.Unlock()
	if failedCleanup {
		return errors.New("engine_cleanup_failed")
	}
	if e := os.MkdirAll(c.Root, 0700); e != nil {
		return e
	}
	path := filepath.Join(c.Root, p.ID+".json")
	if old, e := os.ReadFile(path); e == nil {
		var prev site.Profile
		if json.Unmarshal(old, &prev) != nil {
			return errors.New("site trust changed; create a new site ID")
		}
		if e := site.CheckProfileUpdate(prev, p); e != nil {
			if !replaceTrust || prev.Protocol != p.Protocol || prev.ID != p.ID || p.ConfigRevision <= prev.ConfigRevision {
				return e
			}
		}
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return securefile.Write(path, b, 0600)
}
func (c *Controller) Profiles() ([]site.Profile, error) {
	files, e := filepath.Glob(filepath.Join(c.Root, "*.json"))
	if e != nil {
		return nil, e
	}
	out := []site.Profile{}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		var v site.Profile
		if json.Unmarshal(b, &v) == nil && v.Validate() == nil {
			out = append(out, v)
		}
	}
	return out, nil
}
func (c *Controller) set(gen uint64, fn func(*Status)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.Generation == gen {
		fn(&c.status)
	}
}
func (c *Controller) Disconnect() error { c.op.Lock(); defer c.op.Unlock(); return c.disconnect() }
func (c *Controller) disconnect() error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.status.Generation++
	c.status.State = "disconnected"
	c.status.Error = ""
	c.cancel = nil
	c.done = nil
	c.remote = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(25 * time.Second):
			c.mu.Lock()
			c.cleanupFailed = true
			c.status.State = "failed"
			c.status.Error = "engine_cleanup_failed"
			c.mu.Unlock()
			return errors.New("previous connection cleanup timed out")
		}
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	err := c.Engine.Down(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupFailed = err != nil
	if err != nil {
		c.status.State = "failed"
		c.status.Error = "engine_cleanup_failed"
	}
	return err
}
func (c *Controller) Connect(id, alias, password string, replace bool) error {
	return c.ConnectMode(id, alias, password, replace, false)
}
func (c *Controller) ConnectMode(id, alias, password string, replace, internet bool) error {
	c.op.Lock()
	defer c.op.Unlock()
	if !site.ValidID(id) {
		return errors.New("invalid site")
	}
	if e := c.disconnect(); e != nil {
		return e
	}
	b, e := os.ReadFile(filepath.Join(c.Root, id+".json"))
	if e != nil {
		return e
	}
	var p site.Profile
	if e = site.Decode(bytes.NewReader(b), &p); e != nil {
		return e
	}
	if e = p.Validate(); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.mu.Lock()
	gen, e := nextGeneration(c.Root, c.status.Generation)
	if e != nil {
		c.mu.Unlock()
		cancel()
		return e
	}
	c.status = Status{Generation: gen, State: "probing", SiteID: id, Internet: internet}
	c.cancel = cancel
	c.done = done
	c.mu.Unlock()
	go func() {
		defer close(done)
		e := c.run(ctx, gen, p, alias, &password, replace, internet)
		password = ""
		if e != nil && ctx.Err() == nil {
			c.set(gen, func(s *Status) { s.State = "failed"; s.Error = e.Error() })
		}
	}()
	return nil
}
func (c *Controller) run(ctx context.Context, gen uint64, p site.Profile, alias string, password *string, replace, internet bool) (result error) {
	defer func() { *password = "" }()
	selector, e := controlpath.New(p)
	if e != nil {
		return e
	}
	if _, e = selector.Select(ctx); e != nil {
		return e
	}
	c.set(gen, func(s *Status) {
		s.Control = selector.Current().Kind
		s.ControlCandidate = selector.Current().ID
		s.State = "authenticating"
	})
	private, e := c.Vault.Read(p.ID)
	if e != nil {
		if !os.IsNotExist(e) {
			return e
		}
		_, private, e = ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		if e = c.Vault.Write(p.ID, private); e != nil {
			return e
		}
	}
	if len(private) != ed25519.PrivateKeySize {
		return errors.New("invalid protected device key")
	}
	pk := ed25519.PrivateKey(private)
	loginCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	wg, e := c.Engine.Prepare(loginCtx, p)
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
	remote := &Remote{HTTP: selector.Client(), Origin: p.Origin, Private: pk}
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
	*password = ""
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
	remote.HTTP = selector.Client()
	if engine, ok := c.Engine.(InternetEngine); ok {
		if e = engine.ConfigureInternet(loginCtx, internet, p.InternetDNS); e != nil {
			return e
		}
	} else if internet {
		return errors.New("internet_not_supported")
	}
	c.set(gen, func(s *Status) { s.Control = selector.Current().Kind; s.ControlCandidate = selector.Current().ID })
	if e = c.Engine.Login(loginCtx, p, auth.IDToken); e != nil {
		return e
	}
	if e = c.Engine.Up(loginCtx, p); e != nil {
		return e
	}
	if e = c.Engine.WaitConnected(loginCtx, p); e != nil {
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
	applyRoutes := func(ctx context.Context) error {
		resources, err := SelectedResources(lease.Resources, internet)
		if err != nil {
			return err
		}
		if err = CheckConflicts(resources); err != nil {
			return err
		}
		return c.Engine.Routes(ctx, resources)
	}
	if e = applyRoutes(loginCtx); e != nil {
		return e
	}
	c.set(gen, func(s *Status) { s.State = "negotiating"; s.Lease = lease })
	remoteConnection := c.attachRemote(ctx, p.ID, auth, pk)
	defer c.detachRemote(remoteConnection)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	lastRenew := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job := <-remoteConnection.jobs:
			remoteConnection.handle(job, remote)
		case <-tick.C:
			if !time.Now().Before(lease.ExpiresAt) {
				return errors.New("lease_expired")
			}
			check, done := context.WithTimeout(ctx, 8*time.Second)
			e = selector.Recover(check)
			done()
			c.set(gen, func(s *Status) { s.Control = selector.Current().Kind; s.ControlCandidate = selector.Current().ID })
			if e != nil {
				c.set(gen, func(s *Status) { s.State = "recovering" })
				continue
			}
			if time.Until(auth.ExpiresAt) < 2*time.Minute && auth.RefreshToken != "" {
				var renewed identity.Result
				if e = remote.Call(ctx, "POST", "/netgo/v2/auth/refresh", map[string]string{"refresh_token": auth.RefreshToken}, &renewed, ""); e != nil {
					c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = "authentication_refresh_failed" })
					continue
				}
				auth = renewed
				remote.Token = renewed.Token
				if e = c.Engine.Login(ctx, p, renewed.IDToken); e != nil {
					continue
				}
				if e = c.Engine.Up(ctx, p); e != nil {
					continue
				}
				if e = applyRoutes(ctx); e != nil {
					continue
				}
			}
			if time.Since(lastRenew) >= 30*time.Second || time.Until(lease.ExpiresAt) < 40*time.Second {
				e = remote.Call(ctx, "POST", "/netgo/v2/sessions/"+lease.ID+"/renew", map[string]any{"Generation": gen}, &lease, "")
				if e != nil {
					c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = e.Error() })
					continue
				}
				lastRenew = time.Now()
				if e = applyRoutes(ctx); e != nil {
					return e
				}
			}
			stateCtx, done := context.WithTimeout(ctx, 5*time.Second)
			ds, de := c.Engine.Status(stateCtx, p)
			done()
			if de != nil || !strings.EqualFold(ds.State, "connected") {
				recovery, done := context.WithTimeout(ctx, 20*time.Second)
				de = c.Engine.Login(recovery, p, auth.IDToken)
				if de == nil {
					de = c.Engine.Up(recovery, p)
				}
				if de == nil {
					de = applyRoutes(recovery)
				}
				done()
				c.set(gen, func(s *Status) { s.State = "recovering"; s.Error = "engine_recovering" })
				continue
			}
			c.set(gen, func(s *Status) {
				s.Lease = lease
				if de != nil {
					s.State = "recovering"
					return
				}
				s.Data = ds
				s.Error = ""
				if ds.Path == "negotiating" {
					s.State = "negotiating"
				} else {
					s.State = "connected"
				}
			})
		}
	}
}
func CheckConflicts(resources []model.Resource) error {
	addrs, e := net.InterfaceAddrs()
	if e != nil {
		return e
	}
	for _, a := range addrs {
		local, e := netip.ParsePrefix(a.String())
		if e != nil || local.Addr().IsLoopback() {
			continue
		}
		for _, r := range resources {
			if r.Protocol == "internet" && r.CIDR == "0.0.0.0/0" {
				continue
			}
			remote, e := netip.ParsePrefix(r.CIDR)
			if e != nil {
				return e
			}
			if local.Overlaps(remote) && remote.Bits() != 32 {
				return fmt.Errorf("route_conflict: %s overlaps local %s", remote, local)
			}
		}
	}
	return nil
}
