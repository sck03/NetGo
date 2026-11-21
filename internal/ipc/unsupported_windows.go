//go:build windows

// These stubs let server behavior tests run on the Windows development host.
// Product Windows IPC is implemented only in apps/windows/src/platform.cpp.
package ipc

import (
	"context"
	"errors"
	"net"
)

func Listen(string, string) (net.Listener, error) {
	return nil, errors.New("Linux server IPC is unavailable on Windows")
}
func Dial(context.Context, string) (net.Conn, error) {
	return nil, errors.New("Linux server IPC is unavailable on Windows")
}
