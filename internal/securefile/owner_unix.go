//go:build !windows

package securefile

import (
	"os"
	"syscall"
)

func preserveOwner(f *os.File, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	// Unprivileged profile writes keep the current owner automatically.
	if os.Geteuid() != 0 {
		return nil
	}
	return f.Chown(int(st.Uid), int(st.Gid))
}
