package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedRoundtripTamperAndWrongPassword(t *testing.T) {
	pass := []byte("a-long-test-password")
	b, e := Encrypt([]byte("sensitive configuration"), pass)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := Decrypt(b, pass)
	if e != nil || string(plain) != "sensitive configuration" {
		t.Fatal(e)
	}
	if _, e = Decrypt(b, []byte("wrong password")); e == nil {
		t.Fatal("wrong password accepted")
	}
	b[len(b)-1] ^= 1
	if _, e = Decrypt(b, pass); e == nil {
		t.Fatal("tampering accepted")
	}
}
func TestSameBasenameRootsAreDistinctAndMapped(t *testing.T) {
	directory := t.TempDir()
	roots := []string{filepath.Join(directory, "etc", "netgo"), filepath.Join(directory, "var", "lib", "netgo")}
	for i, root := range roots {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(root, "data"), []byte{byte(i)}, 0600)
	}
	archive, err := Archive(roots)
	if err != nil {
		t.Fatal("default production roots could not be backed up", err)
	}
	dest := filepath.Join(t.TempDir(), "restore")
	if err = Restore(archive, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Roots map[string]string `json:"archive_root_map"`
	}
	if err = json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Roots) != 2 {
		t.Fatal(manifest)
	}
	for name, source := range manifest.Roots {
		b, err := os.ReadFile(filepath.Join(dest, name, "data"))
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile(filepath.Join(source, "data"))
		if err != nil || !bytes.Equal(b, expected) {
			t.Fatal("backup roots collided", source, b, expected)
		}
	}
}
func TestRestoreRejectsTraversalBeforeWriting(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("../escape")
	f.Write([]byte("bad"))
	z.Close()
	dest := filepath.Join(t.TempDir(), "restore")
	if e := Restore(b.Bytes(), dest); e == nil {
		t.Fatal("path traversal accepted")
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("unsafe restore created output")
	}
}
func TestBackupRestoreIsolatedDirectory(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "data.json"), []byte("{}"), 0600)
	b, e := Archive([]string{root})
	if e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if e = Restore(b, dest); e != nil {
		t.Fatal(e)
	}
	if e = Restore(b, dest); e == nil {
		t.Fatal("existing destination overwritten")
	}
}
