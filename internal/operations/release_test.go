package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testRelease(t *testing.T, version string) (string, string) {
	t.Helper()
	root := t.TempDir()
	m, _ := json.Marshal(ReleaseManifest{Version: version, Protocol: model.Protocol, Schema: model.Schema, OS: "linux", Arch: runtime.GOARCH})
	_ = os.WriteFile(filepath.Join(root, "release.json"), m, 0644)
	var sums strings.Builder
	for _, name := range append(append([]string{}, Products...), "release.json") {
		b := m
		if name != "release.json" {
			b = []byte("test executable: " + name)
			_ = os.WriteFile(filepath.Join(root, name), b, 0755)
		}
		h := sha256.Sum256(b)
		fmt.Fprintf(&sums, "%x  %s\n", h, name)
	}
	b := []byte(sums.String())
	_ = os.WriteFile(filepath.Join(root, "SHA256SUMS"), b, 0644)
	h := sha256.Sum256(b)
	return root, hex.EncodeToString(h[:])
}
func TestReleaseRejectsTamperingAndTraversal(t *testing.T) {
	root, digest := testRelease(t, "test")
	if _, err := Verify(root, digest); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "netgo-server"), []byte("tampered"), 0755)
	if _, err := Verify(root, digest); err == nil {
		t.Fatal("tampered executable accepted")
	}
	root, _ = testRelease(t, "test")
	b := []byte(strings.Repeat("0", 64) + "  ../outside\n")
	_ = os.WriteFile(filepath.Join(root, "SHA256SUMS"), b, 0644)
	h := sha256.Sum256(b)
	if _, err := Verify(root, hex.EncodeToString(h[:])); err == nil {
		t.Fatal("traversal accepted")
	}
}
func TestReleaseRecoveryOnHealthFailure(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink replacement uses Linux rename semantics")
	}
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "releases"), 0755)
	old := filepath.Join(root, "releases", "old")
	_ = os.Mkdir(old, 0755)
	_ = os.Symlink(old, filepath.Join(root, "current"))
	source, digest := testRelease(t, "next")
	config := filepath.Join(t.TempDir(), "site.json")
	b, _ := json.Marshal(configuration())
	_ = os.WriteFile(config, b, 0600)
	snapshots := 0
	tx := ReleaseTransaction{Root: root, ConfigPath: config, Run: func(context.Context, string, ...string) error { return nil }, Snapshot: func(context.Context) (string, error) { snapshots++; return "/backup/encrypted", nil }, Health: func(context.Context, site.Config) error {
		current, _ := filepath.EvalSymlinks(filepath.Join(root, "current"))
		if current != old {
			return fmt.Errorf("unhealthy")
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Upgrade(ctx, source, digest); err == nil {
		t.Fatal("health failure ignored")
	}
	current, _ := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if current != old || snapshots != 1 {
		t.Fatal(current, snapshots)
	}
}
