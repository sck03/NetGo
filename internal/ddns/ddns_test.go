package ddns

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture() Config {
	return Config{ZoneID: "zone", RecordID: "record", Name: "control.example", Type: "A", TokenFile: "protected", IntervalSeconds: 60,
		Witnesses: []string{"https://one.example/ip", "https://two.example/ip"}}
}

func TestWitnessAgreementAndRecordProtection(t *testing.T) {
	for _, test := range []struct {
		name, first, second, record string
		fail, patch                 bool
	}{
		{"changed", "8.8.8.8", "8.8.8.8", `{"success":true,"result":{"name":"control.example","type":"A","content":"1.1.1.1","proxied":false}}`, false, true},
		{"unchanged", "8.8.8.8", "8.8.8.8", `{"success":true,"result":{"name":"control.example","type":"A","content":"8.8.8.8","proxied":false}}`, false, false},
		{"disagree", "8.8.8.8", "1.1.1.1", "", true, false},
		{"private", "10.0.0.1", "10.0.0.1", "", true, false},
		{"cgnat", "100.64.0.1", "100.64.0.1", "", true, false},
		{"documentation", "203.0.113.1", "203.0.113.1", "", true, false},
		{"family", "2001:4860:4860::8888", "2001:4860:4860::8888", "", true, false},
		{"wrong_record", "8.8.8.8", "8.8.8.8", `{"success":true,"result":{"name":"other.example","type":"A","content":"1.1.1.1"}}`, true, false},
		{"proxied", "8.8.8.8", "8.8.8.8", `{"success":true,"result":{"name":"control.example","type":"A","content":"1.1.1.1","proxied":true}}`, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			patches := 0
			c := Cloudflare{Config: fixture(), Token: "dns-token"}
			c.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				body := test.first
				if r.URL.Host == "two.example" {
					body = test.second
				}
				if r.URL.Host != "api.cloudflare.com" {
					if r.Header.Get("Authorization") != "" {
						t.Fatal("DNS credential sent to a witness")
					}
				} else {
					if r.Header.Get("Authorization") != "Bearer dns-token" {
						t.Fatal("DNS API request not authenticated")
					}
					body = test.record
					if r.Method == "PATCH" {
						patches++
						body = `{"success":true}`
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			changed, err := c.Update(context.Background())
			if (err != nil) != test.fail || changed != test.patch || patches != map[bool]int{true: 1, false: 0}[test.patch] {
				t.Fatalf("changed=%v patches=%d error=%v", changed, patches, err)
			}
		})
	}
}

func TestDistinctWitnessesAndBoundedInterval(t *testing.T) {
	c := fixture()
	c.Witnesses[1] = "https://one.example/different-path"
	if c.Validate() == nil {
		t.Fatal("same witness counted twice")
	}
	c = fixture()
	c.IntervalSeconds = 0
	if c.Validate() == nil {
		t.Fatal("unbounded check frequency accepted")
	}
}

func TestWitnessDialUsesRecordAddressFamily(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, family := range []string{"A", "AAAA"} {
		c := fixture()
		c.Type = family
		client := New(c, "token")
		transport := client.WitnessClient.Transport.(*http.Transport)
		if transport.Proxy != nil {
			t.Fatal("witness may observe HTTP proxy address")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := transport.DialContext(ctx, "tcp", listener.Addr().String())
		cancel()
		if conn != nil {
			conn.Close()
		}
		if (err == nil) != (family == "A") {
			t.Fatalf("family %s used the wrong transport: %v", family, err)
		}
	}
}
