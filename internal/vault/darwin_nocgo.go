//go:build darwin && !cgo

package vault

import "errors"

type Vault struct{ Root string }

func (Vault) Read(string) ([]byte, error) {
	return nil, errors.New("macOS client requires native Keychain support")
}
func (Vault) Write(string, []byte) error {
	return errors.New("macOS client requires native Keychain support")
}
