package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"netgo/internal/identity"
	"netgo/internal/site"
	"netgo/internal/vault"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRemoteWorkerDoesNotWaitForVPNMaintenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/netgo/v2/remote" {
			t.Error(r.URL.Path)
		}
		_, _ = w.Write([]byte("{\"hosts\":[]}"))
	}))
	defer server.Close()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	c := &Controller{}
	connection := c.attachRemote(context.Background(), "s", "scope", identity.Result{}, key, &Remote{HTTP: server.Client(), Origin: server.URL})
	defer c.detachRemote(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// No connection-maintenance loop drains jobs in this test.
	result, err := c.RemoteCommand(ctx, RemoteRequest{Site: "s", Method: "GET", Path: "/netgo/v2/remote"})
	if err != nil || string(result) != "{\"hosts\":[]}" {
		t.Fatalf("independent worker: %s %v", result, err)
	}
}

func TestRemoteWorkerCancelsInFlightRequestOnDetach(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	c := &Controller{}
	connection := c.attachRemote(context.Background(), "s", "scope", identity.Result{}, key, &Remote{HTTP: server.Client(), Origin: server.URL})
	result := make(chan error, 1)
	go func() {
		_, err := c.RemoteCommand(context.Background(), RemoteRequest{Site: "s", Method: "GET", Path: "/netgo/v2/remote"})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not dispatched")
	}
	detached := make(chan struct{})
	go func() { c.detachRemote(connection); close(detached) }()
	select {
	case <-detached:
	case <-time.After(time.Second):
		t.Fatal("detach did not cancel HTTP")
	}
	if err := <-result; err == nil {
		t.Fatal("disconnected request succeeded")
	}
}

func TestRemoteCredentialRotationWaitsForInFlightProof(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(entered)
			<-release
		}
		if r.URL.Path == "/netgo/v2/auth/refresh" {
			json.NewEncoder(w).Encode(identity.Result{Token: "new-token"})
			return
		}
		want := "Bearer old-token"
		if r.URL.Path == "/after" {
			want = "Bearer new-token"
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("unexpected credential: %s", r.URL.Path)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	remote := &Remote{HTTP: server.Client(), Origin: server.URL, Token: "old-token", Private: key}
	request := make(chan error, 1)
	go func() { request <- remote.Call(context.Background(), "GET", "/slow", nil, nil, "") }()
	<-entered
	rotated := make(chan error, 1)
	go func() { _, err := remote.Refresh(context.Background(), "refresh"); rotated <- err }()
	select {
	case <-rotated:
		t.Fatal("credential rotated under an in-flight request")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-request; err != nil {
		t.Fatal(err)
	}
	if err := <-rotated; err != nil {
		t.Fatal(err)
	}
	if err := remote.Call(context.Background(), "GET", "/after", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteCannotProxyUnrelatedAPI(t *testing.T) {
	c := &Controller{}
	for _, req := range []RemoteRequest{{Method: "GET", Path: "/netgo/v2/admin/users"}, {Method: "POST", Path: "/netgo/v2/remote/sessions/../../admin"}, {Method: "PUT", Path: "/netgo/v2/remote/sessions/AAAAAAAAAAAAAAAAAAAAAA"}, {Method: "GET", Path: "/netgo/v2/remote/sessions/AAAAAAAAAAAAAAAAAAAAAA/consume"}} {
		if _, e := c.RemoteCommand(context.Background(), req); e == nil || e.Error() != "remote_invalid_operation" {
			t.Fatalf("accepted unrelated API: %+v %v", req, e)
		}
	}
}
func TestRemotePendingCommandEndsOnLogout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Controller{remote: &remoteConnection{ctx: ctx, site: "s", jobs: make(chan remoteJob, 8)}}
	finished := make(chan error, 1)
	go func() {
		_, err := c.RemoteCommand(context.Background(), RemoteRequest{Site: "s", Method: "GET", Path: "/netgo/v2/remote"})
		finished <- err
	}()
	select {
	case <-c.remote.jobs:
	case <-time.After(time.Second):
		t.Fatal("command not queued")
	}
	cancel()
	select {
	case e := <-finished:
		if e == nil || e.Error() != "remote_site_disconnected" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("logout did not cancel command")
	}
}
func TestPossessionSignerRetainsDeviceKeyAndRejectsOtherDomains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows device keys are handled by the native Credential Manager adapter")
	}
	pub, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	scope := site.AccountScope("s", "alice")
	c := &Controller{Vault: vault.Vault{Root: t.TempDir()}, remote: &remoteConnection{ctx: context.Background(), site: "s", scope: scope}}
	if e = c.Vault.Write(scope, private.Seed()); e != nil {
		t.Fatal(e)
	}
	message := "NetGo remote possession v1\x00" + strings.Repeat("x", 96)
	signed, e := c.SignPossession("s", message)
	if e != nil {
		t.Fatal(e)
	}
	signature, e := base64.RawURLEncoding.DecodeString(signed)
	if e != nil || !ed25519.Verify(pub, []byte(message), signature) {
		t.Fatal("device signature invalid")
	}
	for _, value := range []string{message[:122], strings.Repeat("x", 123)} {
		if _, e := c.SignPossession("s", value); e == nil {
			t.Fatal("signed another domain")
		}
	}
	if _, e := c.SignPossession("another-site", message); e == nil {
		t.Fatal("signed disconnected site")
	}
	otherPublic, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherScope := site.AccountScope("s", "bob")
	if err := c.Vault.Write(otherScope, otherKey.Seed()); err != nil {
		t.Fatal(err)
	}
	c.remote.scope = otherScope
	signed, e = c.SignPossession("s", message)
	if e != nil {
		t.Fatal(e)
	}
	signature, e = base64.RawURLEncoding.DecodeString(signed)
	if e != nil || !ed25519.Verify(otherPublic, []byte(message), signature) || ed25519.Verify(pub, []byte(message), signature) {
		t.Fatal("account switch signed with the previous account's device")
	}
}
