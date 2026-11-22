package operations

import (
	"context"
	"encoding/json"
	"errors"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configuration() site.Config {
	return site.Config{Site: site.Profile{Protocol: 1, ID: "test", Origin: "https://site.test", Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: "site.test:443"}}}, Listen: "127.0.0.1:8081", Database: "/var/lib/netgo/netgo.db", ManagementURL: "http://127.0.0.1:8080", ManagementTokenFile: "/etc/netgo/management.token", NativeSecretFile: "/etc/netgo/native.secret", HelperSocket: "/run/netgo-helper/helper.sock", Interface: "wt0", LANInterface: "eth0", OverlayCIDR: "100.96.0.0/16", LeaseSeconds: 120}
}
func TestConfigurationFailureRestoresPreviousAndKeepsIdentity(t *testing.T) {
	old := configuration()
	next := old
	next.LeaseSeconds = 90
	path := filepath.Join(t.TempDir(), "site.json")
	data, _ := json.Marshal(old)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	transaction := ConfigTransaction{Run: func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}, Health: func(context.Context, site.Config) error { return nil }, Reconcile: func(_ context.Context, c site.Config) error {
		if c.LeaseSeconds == 90 {
			return errors.New("upstream failed")
		}
		return nil
	}}
	if err := transaction.Apply(context.Background(), path, next); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatal(err)
	}
	restored, err := site.Load(path)
	if err != nil || restored.LeaseSeconds != 120 {
		t.Fatal(restored, err)
	}
	if _, err := os.Stat(path + ".transaction"); !os.IsNotExist(err) {
		t.Fatal("successful recovery left lock")
	}
	if len(calls) != 4 {
		t.Fatal(calls)
	}
	next.Site.Origin = "https://attacker.test"
	if _, err := Plan(path, old, next); err == nil {
		t.Fatal("identity changed without migration")
	}
}
func TestInterruptedTransactionIsNeverOverwritten(t *testing.T) {
	c := configuration()
	path := filepath.Join(t.TempDir(), "site.json")
	b, _ := json.Marshal(c)
	_ = os.WriteFile(path, b, 0600)
	_ = os.Mkdir(path+".transaction", 0700)
	next := c
	next.LeaseSeconds = 60
	called := false
	tx := ConfigTransaction{Run: func(context.Context, string, ...string) error { called = true; return nil }}
	if err := tx.Apply(context.Background(), path, next); err == nil || called {
		t.Fatal("interrupted journal ignored", err)
	}
}

func TestConfigurationAndReleaseOperationsCannotOverlap(t *testing.T) {
	old := configuration()
	path := filepath.Join(t.TempDir(), "site.json")
	data, _ := json.Marshal(old)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	next := old
	next.LeaseSeconds = 90
	entered, release := make(chan struct{}), make(chan struct{})
	first := true
	tx := ConfigTransaction{Run: func(context.Context, string, ...string) error {
		if first {
			first = false
			close(entered)
			<-release
		}
		return nil
	}, Health: func(context.Context, site.Config) error { return nil }}
	done := make(chan error, 1)
	go func() { done <- tx.Apply(context.Background(), path, next) }()
	<-entered
	queued := old
	queued.LeaseSeconds = 60
	err := (ConfigTransaction{}).Apply(context.Background(), path, queued)
	releaseErr := (ReleaseTransaction{ConfigPath: path}).switchTo(context.Background(), "unused", false)
	close(release)
	if firstErr := <-done; firstErr != nil {
		t.Fatal(firstErr)
	}
	if err == nil || releaseErr == nil || !strings.Contains(err.Error(), "operation-lock") || !strings.Contains(releaseErr.Error(), "operation-lock") {
		t.Fatalf("overlapping operations admitted: config=%v release=%v", err, releaseErr)
	}
	current, err := site.Load(path)
	if err != nil || current.LeaseSeconds != 90 {
		t.Fatal("queued operation replaced committed configuration", err)
	}
}

func TestCandidateRollbackPublishesHigherRevision(t *testing.T) {
	old := configuration()
	old.Site.ConfigRevision = 2
	next := old
	next.Site.ConfigRevision = 3
	next.Site.Candidates = []site.Candidate{{ID: "new", Kind: "direct", Address: "new.test:443"}}
	path := filepath.Join(t.TempDir(), "site.json")
	b, _ := json.Marshal(old)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	tx := ConfigTransaction{Run: func(context.Context, string, ...string) error { return nil }, Health: func(context.Context, site.Config) error { return nil }, Reconcile: func(_ context.Context, c site.Config) error {
		if c.Site.ConfigRevision == 3 {
			return errors.New("new deployment failed")
		}
		return nil
	}}
	if err := tx.Apply(context.Background(), path, next); err == nil {
		t.Fatal("failure hidden")
	}
	recovered, err := site.Load(path)
	if err != nil || recovered.Site.ConfigRevision != 4 || !site.SameProfileContent(recovered.Site, old.Site) {
		t.Fatalf("incorrect recovery: %+v %v", recovered.Site, err)
	}
	if err = site.CheckProfileUpdate(next.Site, recovered.Site); err != nil {
		t.Fatal("client cannot accept rollback", err)
	}
}
