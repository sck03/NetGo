package netbird

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"netgo/internal/session"
)

type Management struct {
	URL, Token string
	Client     *http.Client
}

func (m *Management) Request(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	var b []byte
	if in != nil {
		b, _ = json.Marshal(in)
	}
	req, e := http.NewRequestWithContext(ctx, method, m.URL+"/api"+path, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Token "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := m.Client.Do(req)
	if e != nil {
		return nil, errors.New("management unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("management returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); e != nil {
			return nil, e
		}
	}
	return resp.Header, nil
}
func (m *Management) Verify(ctx context.Context, id string) (session.Binding, error) {
	var p struct {
		ID        string `json:"id"`
		UserID    string `json:"user_id"`
		IP        string `json:"ip"`
		Connected bool   `json:"connected"`
		Expired   bool   `json:"login_expired"`
	}
	h, e := m.Request(ctx, "GET", "/peers/"+url.PathEscape(id), nil, &p)
	if e != nil {
		return session.Binding{}, e
	}
	key := h.Get("X-NetGo-Peer-Key")
	if key == "" {
		return session.Binding{}, errors.New("NetGo upstream binding patch is required")
	}
	return session.Binding{ID: p.ID, UserID: p.UserID, IP: p.IP, PublicKey: key, Connected: p.Connected, Expired: p.Expired}, nil
}
func (m *Management) UserActive(ctx context.Context, id string) error {
	var all []struct {
		ID      string `json:"id"`
		Blocked bool   `json:"is_blocked"`
		Status  string `json:"status"`
	}
	_, e := m.Request(ctx, "GET", "/users", nil, &all)
	if e != nil {
		return e
	}
	for _, u := range all {
		if u.ID == id && !u.Blocked && u.Status == "active" {
			return nil
		}
	}
	return errors.New("upstream user unavailable/blocked")
}
func (m *Management) FindPeer(ctx context.Context, user, key string) (session.Binding, error) {
	var all []struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if _, e := m.Request(ctx, "GET", "/peers", nil, &all); e != nil {
		return session.Binding{}, e
	}
	for _, v := range all {
		if v.UserID != user {
			continue
		}
		p, e := m.Verify(ctx, v.ID)
		if e != nil {
			return p, e
		}
		if p.PublicKey == key {
			return p, nil
		}
	}
	return session.Binding{}, errors.New("registered peer not found")
}
