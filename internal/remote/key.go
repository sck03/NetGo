package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"os"
)

// The key is separate from both the database and identity-provider secret. Never
// replace an existing malformed key, which would silently break site trust.
func LoadKey(path string) (ed25519.PrivateKey, error) {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e == nil {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err == nil {
			_, err = f.Write(key.Seed())
		}
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			return nil, err
		}
		return key, nil
	}
	if !errors.Is(e, os.ErrExist) {
		return nil, e
	}
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() != ed25519.SeedSize {
		return nil, errors.New("invalid_remote_signing_key")
	}
	f, e = os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	seed, e := io.ReadAll(io.LimitReader(f, ed25519.SeedSize+1))
	if e != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid_remote_signing_key")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
