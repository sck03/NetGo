// Package mobile binds the existing controller to native mobile platform IO.
// It introduces no second login flow, password database, or control protocol.
package mobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"netgo/internal/client"
	"netgo/internal/site"
	"os"
	"sync"
	"time"
)

type DeviceStore interface {
	ReadKey(site string) ([]byte, error)
	WriteKey(site string, key []byte) error
}
type deviceStore struct{ DeviceStore }

func (s deviceStore) Read(id string) ([]byte, error) {
	key, err := s.ReadKey(id)
	if err == nil && len(key) == 0 {
		return nil, os.ErrNotExist
	}
	return key, err
}
func (s deviceStore) Write(id string, key []byte) error { return s.WriteKey(id, key) }

type Client struct {
	controller    *client.Controller
	mu            sync.Mutex
	closeNative   func()
	networkChange func(bool, string)
}

func (c *Client) ImportProfile(text string) error {
	if len(text) > 65536 {
		return errors.New("profile_too_large")
	}
	var profile site.Profile
	if err := site.Decode(bytes.NewBufferString(text), &profile); err != nil {
		return err
	}
	return c.controller.Import(profile)
}
func (c *Client) Profiles() (string, error) {
	p, err := c.controller.Profiles()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	return string(b), err
}
func (c *Client) Status() string { b, _ := json.Marshal(c.controller.Status()); return string(b) }
func (c *Client) Connect(site, alias, password string, replace bool) error {
	return c.controller.Connect(site, alias, password, replace)
}
func (c *Client) ConnectMode(site, alias, password string, replace, internet bool) error {
	return c.controller.ConnectMode(site, alias, password, replace, internet)
}
func (c *Client) Disconnect() error { return c.controller.Disconnect() }
func (c *Client) RemoteContext(site string) (string, error) {
	v, err := c.controller.RemoteContext(site)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	return string(b), err
}
func (c *Client) RemoteCall(site, method, path, body, key string) (string, error) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := c.controller.RemoteCommand(ctx, client.RemoteRequest{Site: site, Method: method, Path: path, Body: data, Idempotency: key})
	return string(result), err
}
func (c *Client) SignPossession(site string, message []byte) ([]byte, error) {
	signed, err := c.controller.SignPossession(site, string(message))
	if err != nil {
		return nil, err
	}
	return base64.RawURLEncoding.DecodeString(signed)
}
func (c *Client) NetworkChanged(available bool, dns string) {
	c.mu.Lock()
	notify := c.networkChange
	c.mu.Unlock()
	if notify != nil {
		notify(available, dns)
	}
}
func (c *Client) Close() error {
	err := c.Disconnect()
	c.mu.Lock()
	cleanup := c.closeNative
	c.closeNative = nil
	c.networkChange = nil
	c.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
	return err
}
