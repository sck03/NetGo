package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"netgo/internal/vault"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
	c := &Controller{Vault: vault.Vault{Root: t.TempDir()}, remote: &remoteConnection{ctx: context.Background(), site: "s"}}
	if e = c.Vault.Write("s", private.Seed()); e != nil {
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
}
