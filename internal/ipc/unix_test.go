//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func socketTestDir(t *testing.T) string {
	t.Helper()
	// Darwin's Unix address limit is shorter than long test-name temp paths.
	dir, err := os.MkdirTemp("", "ng-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSocketRecoveryAndOwnership(t *testing.T) {
	dir := socketTestDir(t)
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path, owner := filepath.Join(dir, "client.sock"), strconv.Itoa(os.Geteuid())
	// Simulate the socket left behind by a process killed without cleanup.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err = stale.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := Listen(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Listen(path, owner); err == nil {
		second.Close()
		t.Fatal("allowed a second owner")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal("close is not idempotent", err)
	}
	second, err := Listen(path, owner)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	second.Close()
	if err = os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if listener, err := Listen(path, owner); err == nil {
		listener.Close()
		t.Fatal("removed a regular file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatal("changed unrelated file", err)
	}
}

func TestSocketPreservesActiveListenerWithoutLock(t *testing.T) {
	dir := socketTestDir(t)
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "client.sock")
	active, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if listener, err := Listen(path, strconv.Itoa(os.Geteuid())); err == nil {
		listener.Close()
		t.Fatal("replaced active listener")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
