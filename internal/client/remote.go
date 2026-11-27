package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"netgo/internal/identity"
	"regexp"
	"strings"
)

type RemoteRequest struct {
	Site        string          `json:"site"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Body        json.RawMessage `json:"body"`
	Idempotency string          `json:"idempotency"`
}
type remoteResult struct {
	value json.RawMessage
	err   error
}
type remoteJob struct {
	request RemoteRequest
	ctx     context.Context
	result  chan remoteResult
}
type remoteConnection struct {
	ctx                               context.Context
	site, scope, device, user, public string
	jobs                              chan remoteJob
	cancel                            context.CancelFunc
	done                              chan struct{}
}

var remoteInventoryPath = regexp.MustCompile(`^/netgo/v2/remote(\?after=[A-Za-z0-9_-]{1,128})?$`)

var remoteSessionPath = regexp.MustCompile(`^/netgo/v2/remote/sessions/[A-Za-z0-9_-]{22}(/(consume|renew))?$`)

func remoteAllowed(method, path string) bool {
	if method == "GET" && remoteInventoryPath.MatchString(path) {
		return true
	}
	if method == "PUT" && path == "/netgo/v2/remote/host" {
		return true
	}
	if method == "POST" && (path == "/netgo/v2/remote/host/heartbeat" || path == "/netgo/v2/remote/sessions") {
		return true
	}
	if !remoteSessionPath.MatchString(path) {
		return false
	}
	action := strings.HasSuffix(path, "/consume") || strings.HasSuffix(path, "/renew")
	return action && method == "POST" || !action && (method == "GET" || method == "DELETE")
}

// RemoteCommand borrows the connected worker's identity and transport. It never
// exposes a bearer token or private key, and cannot proxy arbitrary API paths.
func (c *Controller) RemoteCommand(ctx context.Context, req RemoteRequest) (json.RawMessage, error) {
	if !remoteAllowed(req.Method, req.Path) || len(req.Body) > 65536 || len(req.Idempotency) > 128 {
		return nil, errors.New("remote_invalid_operation")
	}
	c.mu.Lock()
	connection := c.remote
	c.mu.Unlock()
	if connection == nil || connection.site != req.Site {
		return nil, errors.New("remote_site_disconnected")
	}
	job := remoteJob{request: req, ctx: ctx, result: make(chan remoteResult, 1)}
	select {
	case connection.jobs <- job:
	case <-connection.ctx.Done():
		return nil, errors.New("remote_site_disconnected")
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, errors.New("remote_busy")
	}
	select {
	case result := <-job.result:
		return result.value, result.err
	case <-connection.ctx.Done():
		return nil, errors.New("remote_site_disconnected")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c *Controller) RemoteContext(site string) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	connection := c.remote
	if connection == nil || connection.site != site || connection.ctx.Err() != nil {
		return nil, errors.New("remote_site_disconnected")
	}
	return map[string]any{"site_id": site, "user_id": connection.user, "device_id": connection.device, "public_key": connection.public, "generation": c.status.Generation, "lease": c.status.Lease}, nil
}
func (c *Controller) SignPossession(site, message string) (string, error) {
	if len(message) != 123 || !strings.HasPrefix(message, "NetGo remote possession v1\x00") {
		return "", errors.New("remote_invalid_proof")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	connection := c.remote
	if connection == nil || connection.site != site || connection.ctx.Err() != nil {
		return "", errors.New("remote_site_disconnected")
	}
	seed, err := c.Vault.Read(connection.scope)
	if err != nil {
		return "", err
	}
	defer func() {
		for i := range seed {
			seed[i] = 0
		}
	}()
	if len(seed) != ed25519.SeedSize {
		return "", errors.New("remote_device_key_missing")
	}
	private := ed25519.NewKeyFromSeed(seed)
	defer func() {
		for i := range private {
			private[i] = 0
		}
	}()
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(message))), nil
}
func (c *Controller) attachRemote(ctx context.Context, site, scope string, auth identity.Result, pk ed25519.PrivateKey, transport *Remote) *remoteConnection {
	ctx, cancel := context.WithCancel(ctx)
	connection := &remoteConnection{ctx: ctx, cancel: cancel, done: make(chan struct{}), site: site, scope: scope, device: auth.DeviceID, user: auth.User.ID, public: base64.RawURLEncoding.EncodeToString(pk.Public().(ed25519.PublicKey)), jobs: make(chan remoteJob, 8)}
	// VPN recovery and route delivery can each wait several seconds. Desktop
	// heartbeats must not queue behind them on the connection maintenance loop.
	go func() {
		defer close(connection.done)
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-connection.jobs:
				connection.handle(job, transport)
			}
		}
	}()
	c.mu.Lock()
	c.remote = connection
	c.mu.Unlock()
	return connection
}
func (c *Controller) detachRemote(connection *remoteConnection) {
	c.mu.Lock()
	if c.remote == connection {
		c.remote = nil
	}
	c.mu.Unlock()
	connection.cancel()
	<-connection.done
}
func (connection *remoteConnection) handle(job remoteJob, r *Remote) {
	if job.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(job.ctx)
	defer cancel()
	stop := context.AfterFunc(connection.ctx, cancel)
	defer stop()
	var result json.RawMessage
	err := r.Call(ctx, job.request.Method, job.request.Path, job.request.Body, &result, job.request.Idempotency)
	job.result <- remoteResult{result, err}
}
