//go:build !windows

package private

import "os"

func protect(name string, directory bool) error {
	if directory {
		return os.Chmod(name, 0700)
	}
	return os.Chmod(name, 0600)
}
