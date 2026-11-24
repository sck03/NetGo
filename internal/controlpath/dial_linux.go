//go:build linux && !android

package controlpath

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"
)

func dialControl(ctx context.Context, network, address string, budget time.Duration) (net.Conn, error) {
	mark := uint64(0x1bd00)
	if value := os.Getenv("NB_FWMARK_BASE"); value != "" {
		var err error
		mark, err = strconv.ParseUint(value, 0, 32)
		if err != nil || mark == 0 || mark&255 != 0 {
			return nil, errors.New("invalid_control_socket_mark")
		}
	}
	d := net.Dialer{Timeout: budget, KeepAlive: 30 * time.Second}
	d.Control = func(_, _ string, raw syscall.RawConn) error {
		var markError error
		err := raw.Control(func(fd uintptr) {
			markError = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(mark))
		})
		if err != nil {
			return err
		}
		// An unprivileged development process can still use ordinary LAN mode.
		// The installed Linux connection service owns CAP_NET_RAW for VPN bypass.
		if errors.Is(markError, syscall.EPERM) && os.Geteuid() != 0 {
			return nil
		}
		return markError
	}
	return d.DialContext(ctx, network, address)
}
