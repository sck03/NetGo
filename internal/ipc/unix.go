//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The lock inode is permanent. Removing it on shutdown would let two processes
// lock different inodes and both believe they own the socket.
type socketListener struct {
	net.Listener
	lock *os.File
	once sync.Once
	err  error
}

func (l *socketListener) Close() error {
	l.once.Do(func() { l.err = errors.Join(l.Listener.Close(), l.lock.Close()) })
	return l.err
}

func Listen(path, owner string) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("socket path must be absolute")
	}
	if owner != strconv.Itoa(os.Geteuid()) {
		return nil, errors.New("socket owner must be the current user")
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, errors.New("socket directory must be private and owned by the current user")
	}
	fd, e := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	owned := false
	defer func() {
		if !owned {
			_ = lock.Close()
		}
	}()
	var stat unix.Stat_t
	if e = unix.Fstat(fd, &stat); e != nil {
		return nil, e
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Mode&0077 != 0 {
		return nil, errors.New("invalid socket lock file")
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return nil, errors.New("connection service already running")
	}
	if info, e = os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSocket == 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return nil, errors.New("refusing to replace an unowned socket or non-socket path")
		}
		// Also protect an active listener that does not hold our instance lock.
		connection, probe := net.DialTimeout("unix", path, 250*time.Millisecond)
		if probe == nil {
			_ = connection.Close()
			return nil, errors.New("connection service already running")
		}
		if !errors.Is(probe, syscall.ECONNREFUSED) && !errors.Is(probe, os.ErrNotExist) {
			return nil, probe
		}
		if e = os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		l.Close()
		return nil, e
	}
	owned = true
	return &socketListener{Listener: l, lock: lock}, nil
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
