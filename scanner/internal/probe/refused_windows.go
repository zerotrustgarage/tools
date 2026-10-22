//go:build windows

package probe

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isRefused(err error) bool { return errors.Is(err, windows.WSAECONNREFUSED) }
