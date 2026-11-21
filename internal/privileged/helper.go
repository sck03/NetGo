package privileged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"netgo/internal/gateway"
	"netgo/internal/ipc"
	"netgo/internal/model"
	"netgo/internal/site"
	"time"
)

type Client struct{ Path string }

func (c *Client) Apply(ctx context.Context, leases []model.Lease) error {
	b, e := json.Marshal(leases)
	if e != nil {
		return e
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return ipc.Dial(ctx, c.Path) }}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	r, _ := http.NewRequestWithContext(ctx, "POST", "http://helper/apply", bytes.NewReader(b))
	resp, e := cl.Do(r)
	if e != nil {
		return errors.New("gateway helper unavailable")
	}
	defer resp.Body.Close()
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
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var leases []model.Lease
		if e := site.Decode(r.Body, &leases); e != nil || len(leases) > 1024 {
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
