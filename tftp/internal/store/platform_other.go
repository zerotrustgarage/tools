//go:build !linux && !darwin && !windows

package store

import (
	"errors"
	"os"
)

func private(*os.File, bool) error {
	return errors.New("uploads are supported on Linux, macOS and Windows")
}
func freeBytes(*os.File) (uint64, error) {
	return 0, errors.New("free space checks unsupported on this platform")
}
func lockDrop(*os.File) error { return errors.New("drop box locking unsupported on this platform") }
