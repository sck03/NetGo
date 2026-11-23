package operations

import (
	"context"
	"encoding/json"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUpgradeServicePermissionsWithRestrictiveUmask(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "releases", "old")
	if err := os.MkdirAll(old, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	source, digest := testRelease(t, "next")
	config := filepath.Join(t.TempDir(), "site.json")
	data, _ := json.Marshal(configuration())
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	tx := ReleaseTransaction{Root: root, ConfigPath: config,
		Run:      func(context.Context, string, ...string) error { return nil },
		Snapshot: func(context.Context) (string, error) { return "/protected/encrypted-snapshot", nil },
		Health:   func(context.Context, site.Config) error { return nil }}
	if err := tx.Upgrade(context.Background(), source, digest); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "releases", "next")
	for _, name := range append([]string{""}, Products...) {
		info, err := os.Stat(filepath.Join(target, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0755 {
			t.Fatalf("service cannot read/execute %q: %o", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(filepath.Join(root, "previous-release.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("recovery metadata permissions changed", err)
	}
}
