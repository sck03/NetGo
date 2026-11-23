// Package securefile writes owned configuration without partial replacements.
package securefile

import (
	"errors"
	"os"
	"path/filepath"
)

func Write(path string, data []byte, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("target must be a regular file")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".netgo-write-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	defer f.Close()
	if info != nil {
		mode = info.Mode().Perm()
		if err = preserveOwner(f, info); err != nil {
			return err
		}
	}
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func Create(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	// Callers specify the final permissions, just as they do with Write.
	// A restrictive administrative umask must not turn service binaries into
	// owner-only executables; secret callers still explicitly request 0600.
	if err = f.Chmod(mode); err == nil {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return closeErr
}
