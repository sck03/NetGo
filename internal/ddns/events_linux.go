//go:build linux

package ddns

import (
	"context"
	"golang.org/x/sys/unix"
	"time"
)

// NetworkEvents listens for kernel link, address and default-route changes.
// A bounded channel coalesces bursts; the caller keeps its periodic fallback.
func NetworkEvents(ctx context.Context) (<-chan struct{}, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	groups := uint32(unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_IPV4_ROUTE | unix.RTMGRP_IPV6_ROUTE)
	if err = unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: groups}); err != nil {
		unix.Close(fd)
		return nil, err
	}
	events := make(chan struct{}, 1)
	go func() {
		defer unix.Close(fd)
		var last time.Time
		buffer := make([]byte, 64<<10)
		for ctx.Err() == nil {
			ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 1000)
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				return
			}
			if ready == 0 {
				continue
			}
			n, _, err := unix.Recvfrom(fd, buffer, 0)
			if err == unix.EAGAIN || err == unix.EINTR {
				continue
			}
			if err != nil {
				return
			}
			if n > 0 && time.Since(last) >= time.Second {
				last = time.Now()
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return events, nil
}
