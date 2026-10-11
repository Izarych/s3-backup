//go:build linux || darwin

package backup

import "golang.org/x/sys/unix"

func availableSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Bavail accounts for space actually available to the current user.
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
