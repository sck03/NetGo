//go:build !windows

package vault

import (
	"errors"
	"netgo/internal/site"
	"os"
	"path/filepath"
)

type Vault struct{ Root string }

func (v Vault) Read(id string) ([]byte, error) {
	if !site.ValidID(id) {
		return nil, errors.New("invalid vault ID")
	}
	return os.ReadFile(filepath.Join(v.Root, id+".secret"))
}
func (v Vault) Write(id string, b []byte) error {
	if !site.ValidID(id) {
		return errors.New("invalid vault ID")
	}
	if e := os.MkdirAll(v.Root, 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(v.Root, id+".secret"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	return f.Close()
}
