//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
)

func Listen(path, owner string) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("socket path must be absolute")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	if _, e := os.Lstat(path); e == nil {
		return nil, errors.New("socket exists: check running service before removing stale socket")
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
