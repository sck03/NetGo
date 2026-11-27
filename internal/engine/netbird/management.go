package netbird

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"netgo/internal/model"
	"netgo/internal/session"
	"sync/atomic"
)

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string   { return fmt.Sprintf("management returned HTTP %d", e.Status) }
func (e *HTTPError) HTTPStatus() int { return e.Status }

// A connection failure before GotConn cannot have changed upstream state. Once
// a connection was obtained, treat the result as unknown even if writing failed.
type RequestFailure struct{ connected bool }

func (e *RequestFailure) Error() string                 { return "management unavailable" }
func (e *RequestFailure) RequestMayHaveCommitted() bool { return e.connected }

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
	var connected atomic.Bool
	if m.Client.Transport != nil {
		if _, standard := m.Client.Transport.(*http.Transport); !standard {
			// A custom RoundTripper need not emit httptrace callbacks.
			connected.Store(true)
		}
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	}))
	resp, e := m.Client.Do(req)
	if e != nil {
		return nil, &RequestFailure{connected: connected.Load()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode}
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
		var status *HTTPError
		if errors.As(e, &status) && status.Status == http.StatusNotFound {
			return session.Binding{}, model.ErrNotFound
		}
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
	return model.ErrForbidden
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
