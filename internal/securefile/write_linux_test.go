package securefile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCreateFinalPermissionsAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	for name, mode := range map[string]os.FileMode{"secret": 0600, "manifest": 0644, "program": 0755} {
		path := filepath.Join(root, name)
		if err := Create(path, []byte("original"), mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s permissions do not match %o", name, mode)
		}
		if err := Create(path, []byte("replacement"), mode); err == nil {
			t.Fatal("existing file overwritten")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "original" {
			t.Fatal("existing contents changed")
		}
	}
}
