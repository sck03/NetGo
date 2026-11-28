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

func (m *Management) request(ctx context.Context, method, path string, in any) (*http.Response, error) {
	var b []byte
	var err error
	if in != nil {
		b, err = json.Marshal(in)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, m.URL+"/api"+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	var connected atomic.Bool
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	if client.Transport != nil {
		if _, standard := client.Transport.(*http.Transport); !standard {
			connected.Store(true)
		}
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}))
	resp, err := client.Do(req)
	if err != nil {
		return nil, &RequestFailure{connected: connected.Load()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode}
	}
	return resp, nil
}

func (m *Management) Request(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	resp, err := m.request(ctx, method, path, in)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return nil, err
		}
	}
	return resp.Header, nil
}

// Locked upstream exposes list APIs, not arbitrary single-user/key lookups.
// Decode one row at a time and stop on a match; never cache authorization state.
func scanList[T any](ctx context.Context, m *Management, path string, visit func(T) (bool, error)) error {
	resp, err := m.request(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	start, err := dec.Token()
	if err != nil {
		return err
	}
	if start != json.Delim('[') {
		return errors.New("invalid management list")
	}
	for dec.More() {
		var row T
		if err = dec.Decode(&row); err != nil {
			return err
		}
		stop, err := visit(row)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	_, err = dec.Token()
	return err
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
	type user struct {
		ID      string `json:"id"`
		Blocked bool   `json:"is_blocked"`
		Status  string `json:"status"`
	}
	active := false
	err := scanList(ctx, m, "/users", func(u user) (bool, error) {
		if u.ID != id {
			return false, nil
		}
		active = !u.Blocked && u.Status == "active"
		return true, nil
	})
	if err != nil {
		return err
	}
	if !active {
		return model.ErrForbidden
	}
	return nil
}
func (m *Management) FindPeer(ctx context.Context, user, key string) (session.Binding, error) {
	type peer struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	var candidates []string
	err := scanList(ctx, m, "/peers", func(v peer) (bool, error) {
		if v.UserID == user {
			candidates = append(candidates, v.ID)
		}
		return false, nil
	})
	if err != nil {
		return session.Binding{}, err
	}
	// Close the listing response before another request: bounded connection pools
	// must not deadlock when several registrations resolve peers concurrently.
	for _, id := range candidates {
		p, err := m.Verify(ctx, id)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return session.Binding{}, err
		}
		if p.PublicKey == key && p.UserID == user {
			return p, nil
		}
	}
	return session.Binding{}, errors.New("registered peer not found")
}
