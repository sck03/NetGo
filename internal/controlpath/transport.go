package controlpath

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"netgo/internal/protocol"
	"netgo/internal/site"
	"sync"
	"time"
)

type Selector struct {
	Profile       site.Profile
	Roots         *x509.CertPool
	mu            sync.RWMutex
	selected      site.Candidate
	lastSwitch    time.Time
	successes     int
	successID     string
	ProbeTimeout  time.Duration
	FallbackDelay time.Duration
	clocks        map[string]clockSample
	transports    map[string]*http.Transport
}

type clockSample struct{ server, received time.Time }

// Now advances the authenticated server clock using monotonic elapsed time.
// Changing the client's wall clock does not extend an existing lease.
func (s *Selector) Now() time.Time {
	s.mu.RLock()
	sample := s.clocks[s.selected.ID]
	s.mu.RUnlock()
	if sample.received.IsZero() {
		return time.Now()
	}
	return sample.server.Add(time.Since(sample.received))
}

func New(p site.Profile) (*Selector, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	var roots *x509.CertPool
	if p.CA != "" {
		roots, _ = x509.SystemCertPool()
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(p.CA)) {
			return nil, errors.New("invalid CA certificate")
		}
	}
	return &Selector{Profile: p, Roots: roots, ProbeTimeout: 3 * time.Second, FallbackDelay: 800 * time.Millisecond}, nil
}
func (s *Selector) Current() site.Candidate { s.mu.RLock(); defer s.mu.RUnlock(); return s.selected }
func (s *Selector) transport(c site.Candidate, budget time.Duration) *http.Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s/%s/%d", c.ID, c.Address, budget)
	if tr := s.transports[key]; tr != nil {
		return tr
	}
	if s.transports == nil {
		s.transports = map[string]*http.Transport{}
	}
	u, _ := url.Parse(s.Profile.Origin)
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: s.Roots}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialControl(ctx, network, c.Address, budget)
	}, TLSHandshakeTimeout: budget, ResponseHeaderTimeout: budget, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 8, IdleConnTimeout: 45 * time.Second}
	s.transports[key] = tr
	return tr
}
func (s *Selector) probe(ctx context.Context, c site.Candidate) error {
	ctx, cancel := context.WithTimeout(ctx, s.ProbeTimeout)
	defer cancel()
	tr := s.transport(c, s.ProbeTimeout)
	cl := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", s.Profile.Origin+"/netgo/v2/health", nil)
	resp, e := cl.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	var h protocol.Health
	if resp.StatusCode != 200 || json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&h) != nil {
		return errors.New("invalid control health response")
	}
	if e = h.Validate(s.Profile.ID); e != nil {
		return e
	}
	if h.ServerUnix != 0 {
		s.mu.Lock()
		if s.clocks == nil {
			s.clocks = map[string]clockSample{}
		}
		s.clocks[c.ID] = clockSample{time.Unix(h.ServerUnix, 0), time.Now()}
		s.mu.Unlock()
	}
	return nil
}
func (s *Selector) Select(ctx context.Context) (site.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	type result struct {
		c site.Candidate
		e error
	}
	out := make(chan result, len(s.Profile.Candidates))
	incompatible := false
	for _, c := range s.Profile.Candidates {
		go func(c site.Candidate) {
			if c.Kind == "auxiliary" {
				t := time.NewTimer(s.FallbackDelay)
				defer t.Stop()
				select {
				case <-t.C:
				case <-ctx.Done():
					out <- result{c, ctx.Err()}
					return
				}
			}
			out <- result{c, s.probe(ctx, c)}
		}(c)
	}
	for range s.Profile.Candidates {
		select {
		case r := <-out:
			incompatible = incompatible || errors.Is(r.e, protocol.ErrIncompatible)
			if r.e == nil {
				s.mu.Lock()
				if s.selected.ID != r.c.ID {
					s.lastSwitch = time.Now()
				}
				s.selected = r.c
				s.successes, s.successID = 0, ""
				s.mu.Unlock()
				return r.c, nil
			}
		case <-ctx.Done():
			if incompatible {
				return site.Candidate{}, protocol.ErrIncompatible
			}
			return site.Candidate{}, errors.New("no reachable trusted control entry")
		}
	}
	if incompatible {
		return site.Candidate{}, protocol.ErrIncompatible
	}
	return site.Candidate{}, errors.New("no reachable trusted control entry")
}

// RoundTrip never retries a write. Callers retry only with an idempotency key.
func (s *Selector) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(s.Profile.Origin)
	if r.URL.Host != u.Host || r.URL.Scheme != "https" {
		return nil, errors.New("cross-origin control request rejected")
	}
	c := s.Current()
	if c.ID == "" {
		var e error
		c, e = s.Select(r.Context())
		if e != nil {
			return nil, e
		}
	}
	// Stable per-candidate pools; request contexts enforce each total deadline.
	tr := s.transport(c, 20*time.Second)
	request := r.Clone(r.Context())
	// Signed requests consume a nonce. Even an idempotency key must not permit
	// net/http to replay the same signature on a stale pooled connection.
	request.GetBody = nil
	resp, e := tr.RoundTrip(request)
	if e != nil {
		tr.CloseIdleConnections()
		return nil, fmt.Errorf("control transport unavailable: %w", e)
	}
	return resp, nil
}
func (s *Selector) Client() *http.Client {
	return &http.Client{Transport: s, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("control redirects rejected") }}
}
func (s *Selector) Recover(ctx context.Context) error {
	c := s.Current()
	if c.ID == "" || s.probe(ctx, c) != nil {
		_, e := s.Select(ctx)
		return e
	}
	if c.Kind == "direct" {
		return nil
	}
	// Check all WAN candidates within one probe window. A failed first WAN must
	// not prevent recovery through a healthy second/third WAN.
	results := make(chan site.Candidate, len(s.Profile.Candidates))
	count := 0
	for _, candidate := range s.Profile.Candidates {
		if candidate.Kind == "direct" {
			count++
			go func(d site.Candidate) {
				if s.probe(ctx, d) != nil {
					d = site.Candidate{}
				}
				results <- d
			}(candidate)
		}
	}
	healthy := map[string]bool{}
	for range count {
		d := <-results
		if d.ID != "" {
			healthy[d.ID] = true
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var chosen site.Candidate
	for _, d := range s.Profile.Candidates {
		if healthy[d.ID] && (chosen.ID == "" || d.ID == s.successID) {
			chosen = d
		}
	}
	if chosen.ID == "" {
		s.successes, s.successID = 0, ""
		return nil
	}
	if s.successID != chosen.ID {
		s.successes, s.successID = 0, chosen.ID
	}
	s.successes++
	if s.successes >= 3 && time.Since(s.lastSwitch) >= 30*time.Second {
		s.selected, s.lastSwitch = chosen, time.Now()
		s.successes, s.successID = 0, ""
	}
	return nil
}
func (s *Selector) Dial(ctx context.Context) (net.Conn, error) {
	c := s.Current()
	if c.ID == "" {
		var e error
		c, e = s.Select(ctx)
		if e != nil {
			return nil, e
		}
	}
	return dialControl(ctx, "tcp", c.Address, 3*time.Second)
}

func (s *Selector) CloseIdleConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, tr := range s.transports {
		tr.CloseIdleConnections()
		delete(s.transports, key)
	}
}
