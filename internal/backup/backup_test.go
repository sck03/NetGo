package backup

import (
	"archive/zip"
	"bytes"
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
