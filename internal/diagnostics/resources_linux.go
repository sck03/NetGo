//go:build linux

package diagnostics

import "golang.org/x/sys/unix"

func hostResources(path string) (HostResources, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return HostResources{}, err
	}
	var disk unix.Statfs_t
	if err := unix.Statfs(path, &disk); err != nil {
		return HostResources{}, err
	}
	return HostResources{CPUSeconds: float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6, MaxRSSBytes: uint64(usage.Maxrss) * 1024, DiskAvailableBytes: disk.Bavail * uint64(disk.Bsize), DiskTotalBytes: disk.Blocks * uint64(disk.Bsize)}, nil
}
