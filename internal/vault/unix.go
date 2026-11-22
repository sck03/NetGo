//go:build !windows

package vault

import (
	"errors"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"os"
	"path/filepath"
)

type Vault struct{ Root string }

func (v Vault) Read(id string) ([]byte, error) {
	if !site.ValidID(id) {
		return nil, errors.New("invalid vault ID")
	}
	path := filepath.Join(v.Root, id+".secret")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("unprotected device key")
	}
	return os.ReadFile(path)
}
func (v Vault) Write(id string, b []byte) error {
	if !site.ValidID(id) {
		return errors.New("invalid vault ID")
	}
	if e := os.MkdirAll(v.Root, 0700); e != nil {
		return e
	}
	return securefile.Write(filepath.Join(v.Root, id+".secret"), b, 0600)
}
