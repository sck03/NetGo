package privileged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"netgo/internal/gateway"
	"netgo/internal/ipc"
	"netgo/internal/model"
	"netgo/internal/site"
	"sync"
	"time"
)

type Client struct {
	Path string
	once sync.Once
	http *http.Client
}

func (c *Client) transport() *http.Client {
	c.once.Do(func() {
		path := c.Path
		tr := &http.Transport{MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: time.Minute,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return ipc.Dial(ctx, path) }}
		c.http = &http.Client{Transport: tr, Timeout: 5 * time.Second}
	})
	return c.http
}

func (c *Client) CloseIdleConnections() { c.transport().CloseIdleConnections() }

func (c *Client) Apply(ctx context.Context, leases []model.Lease) error {
	_, err := c.request(ctx, "/apply", leases)
	return err
}
func (c *Client) Renew(ctx context.Context, lease model.Lease) (bool, error) {
	return c.request(ctx, "/renew", []model.Lease{lease})
}
func (c *Client) request(ctx context.Context, path string, leases []model.Lease) (bool, error) {
	if e := gateway.ValidateCapacity(leases); e != nil {
		return false, e
	}
	b, e := json.Marshal(leases)
	if e != nil {
		return false, e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", "http://helper"+path, bytes.NewReader(b))
	if e != nil {
		return false, e
	}
	resp, e := c.transport().Do(r)
	if e != nil {
		return false, errors.New("gateway helper unavailable")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
	if path == "/renew" && resp.StatusCode == http.StatusConflict {
		return false, nil
	}
	if resp.StatusCode != 200 {
		return false, errors.New("gateway policy rejected")
	}
	return true, nil
}
func Handler(g gateway.Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || (r.URL.Path != "/apply" && r.URL.Path != "/renew") {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, gateway.MaxPolicyBytes)
		var leases []model.Lease
		if e := site.Decode(r.Body, &leases); e != nil || gateway.ValidateCapacity(leases) != nil {
			http.Error(w, "invalid leases", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if r.URL.Path == "/renew" {
			if len(leases) != 1 {
				http.Error(w, "one lease required", http.StatusBadRequest)
				return
			}
			renewer, ok := g.(gateway.Renewer)
			if !ok {
				w.WriteHeader(http.StatusConflict)
				return
			}
			handled, err := renewer.Renew(ctx, leases[0])
			if err != nil {
				http.Error(w, "renew failed", http.StatusServiceUnavailable)
				return
			}
			if !handled {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if e := g.Apply(ctx, leases); e != nil {
			http.Error(w, "apply failed", 503)
			return
		}
		w.WriteHeader(200)
	})
}
