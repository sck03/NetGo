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
	if e := gateway.ValidateCapacity(leases); e != nil {
		return e
	}
	b, e := json.Marshal(leases)
	if e != nil {
		return e
	}
	r, _ := http.NewRequestWithContext(ctx, "POST", "http://helper/apply", bytes.NewReader(b))
	resp, e := c.transport().Do(r)
	if e != nil {
		return errors.New("gateway helper unavailable")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != 200 {
		return errors.New("gateway policy rejected")
	}
	return nil
}
func Handler(g gateway.Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/apply" {
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
		if e := g.Apply(ctx, leases); e != nil {
			http.Error(w, "apply failed", 503)
			return
		}
		w.WriteHeader(200)
	})
}
