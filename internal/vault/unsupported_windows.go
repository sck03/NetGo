//go:build windows

package vault

import "errors"

type Vault struct{ Root string }

func (Vault) Read(string) ([]byte, error) {
	return nil, errors.New("Linux CLI vault is unavailable on Windows")
}
func (Vault) Write(string, []byte) error {
	return errors.New("Linux CLI vault is unavailable on Windows")
}
