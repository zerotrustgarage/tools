package store

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Check the normalized on-disk name as well as the requested name. NTFS can
// otherwise expose a private staging directory through its automatic 8.3 alias.
func checkReadable(f *os.File) error {
	b := make([]uint16, 512)
	for {
		n, e := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &b[0], uint32(len(b)), 0)
		if e != nil {
			return e
		}
		if n == 0 || n > 32767 {
			return errors.New("invalid normalized file name")
		}
		if n >= uint32(len(b)) {
			b = make([]uint16, n+1)
			continue
		}
		name := strings.ReplaceAll(windows.UTF16ToString(b[:n]), "\\", "/")
		for _, part := range strings.Split(name, "/") {
			if strings.HasPrefix(strings.ToLower(part), ".ztg-promote-") {
				return errors.New("private promotion staging file")
			}
		}
		return nil
	}
}
