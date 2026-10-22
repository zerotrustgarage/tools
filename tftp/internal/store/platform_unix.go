//go:build linux || darwin

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func private(f *os.File, directory bool) error {
	var stat unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &stat); e != nil {
		return e
	}
	if int(stat.Uid) != os.Geteuid() {
		return errors.New("drop box must be owned by the current user")
	}
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return f.Chmod(mode)
}
func freeBytes(f *os.File) (uint64, error) {
	var stat unix.Statfs_t
	if e := unix.Fstatfs(int(f.Fd()), &stat); e != nil {
		return 0, e
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
func lockDrop(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
