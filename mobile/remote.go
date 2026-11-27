package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"netgo/internal/client"
	"time"
)

// RemoteActivity is owned by one native viewer/host, independently of the VPN.
// Implementations must be thread-safe; no UI-thread dispatch is required.
type RemoteActivity interface{ ControlActive() bool }

func remoteContext(active RemoteActivity, cleanup bool) (context.Context, context.CancelFunc) {
	timeout := 4 * time.Second
	if cleanup {
		timeout = 2 * time.Second
		active = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if active != nil {
		if !active.ControlActive() {
			cancel()
			return ctx, cancel
		}
		go func() {
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if !active.ControlActive() {
						cancel()
						return
					}
				}
			}
		}()
	}
	return ctx, cancel
}

func (c *Client) RemoteCall(site, method, path, body, key string, active RemoteActivity) (string, error) {
	if len(body) > 65536 {
		return "", errors.New("remote_too_large")
	}
	var data json.RawMessage
	if body != "" {
		data = json.RawMessage(body)
		if !json.Valid(data) {
			return "", errors.New("invalid_json")
		}
	}
	ctx, cancel := remoteContext(active, method == "DELETE")
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := c.controller.RemoteCommand(ctx, client.RemoteRequest{Site: site, Method: method, Path: path, Body: data, Idempotency: key})
	return string(result), err
}
