package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"netgo/internal/engine/netbird"
	"netgo/internal/identity"
	"netgo/internal/model"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strconv"
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
	RemoteReady      bool               `json:"remote_ready"`
	ProfilesRevision uint64             `json:"profiles_revision"`
}
type Controller struct {
	mu               sync.Mutex
	op               sync.Mutex
	Root             string
	Vault            DeviceVault
	Engine           Engine
	status           Status
	cancel           context.CancelFunc
	done             chan struct{}
	cleanupFailed    bool
	remote           *remoteConnection
	remember         bool
	profilesRevision uint64
}
type Remote struct {
	auth          sync.RWMutex
	HTTP          *http.Client
	Origin, Token string
	Private       ed25519.PrivateKey
	Now           func() time.Time
}

func terminalControlError(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case "unauthorized", "forbidden", "not_found", "lease_expired", "session_conflict", "internet_forbidden":
		return true
	}
	return false
}

func (r *Remote) Call(ctx context.Context, method, path string, in, out any, key string) error {
	r.auth.RLock()
	defer r.auth.RUnlock()
	return r.call(ctx, method, path, in, out, key)
}

// Rotate credentials between requests. In-flight desktop requests finish with
// their original proof before the server invalidates that credential.
func (r *Remote) Refresh(ctx context.Context, refreshToken string) (identity.Result, error) {
	r.auth.Lock()
	defer r.auth.Unlock()
	var result identity.Result
	err := r.call(ctx, "POST", "/netgo/v2/auth/refresh", map[string]string{"refresh_token": refreshToken}, &result, "")
	if err == nil {
		r.Token = result.Token
	}
	return result, err
}

func (r *Remote) call(ctx context.Context, method, path string, in, out any, key string) error {
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
		now := time.Now()
		if r.Now != nil {
			now = r.Now()
		}
		stamp := strconv.FormatInt(now.Unix(), 10)
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
	s.ProfilesRevision = c.profilesRevision
	s.RemoteReady = c.remote != nil && c.remote.ctx.Err() == nil && c.remote.site == s.SiteID && s.Lease.ID != "" && (s.State == "connected" || s.State == "negotiating")
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
	return c.saveProfile(p)
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
	return c.disconnectWithin(25 * time.Second)
}

func (c *Controller) disconnectWithin(timeout time.Duration) error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.status.Generation++
	c.status.State = "disconnecting"
	c.status.Error = ""
	c.remote = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(timeout):
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
	// Retain the worker fence across retries until deferred cleanup finishes.
	c.cancel = nil
	c.done = nil
	c.cleanupFailed = err != nil
	c.status.State = "disconnected"
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
	return c.connectMode(id, alias, password, replace, internet, false)
}
func (c *Controller) connectMode(id, alias, password string, replace, internet, remember bool) error {
	c.op.Lock()
	defer c.op.Unlock()
	return c.connectLocked(id, alias, password, replace, internet, remember)
}
func (c *Controller) connectLocked(id, alias, password string, replace, internet, remember bool) error {
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
	c.remember = remember
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
