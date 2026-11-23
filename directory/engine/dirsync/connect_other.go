//go:build !windows

package dirsync

import (
	"context"
	"errors"
)

// PowerShellRunner runs the fixed scripts in Windows PowerShell; it exists on
// Windows only (connect_windows.go).
type PowerShellRunner struct {
	// MaxOutput bounds standard output in bytes; DefaultMaxOutput when zero.
	MaxOutput int
}

// DefaultMaxOutput bounds a script's standard output.
const DefaultMaxOutput = 1 << 20

// Run refuses: PowerShell remoting to the sync server runs on Windows only.
func (PowerShellRunner) Run(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("PowerShell remoting to the sync server runs on Windows only")
}
