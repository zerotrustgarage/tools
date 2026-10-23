package main

import (
	"errors"
	"io"
	"os"

	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// ValidateTargets checks and normalizes target syntax without DNS lookups,
// scope checks, or scan side effects.
func (a *App) ValidateTargets(input string) (string, error) {
	return targets.NormalizeList(input)
}

// ImportTargets reads a bounded UTF-8 plain text target list. Choosing no file
// is a normal cancellation and leaves app state untouched.
func (a *App) ImportTargets() (string, error) {
	if a.platform == nil {
		return "", errors.New("opening target lists is not available")
	}
	path, err := a.platform.OpenFile("Import target list", "Text files (*.txt)", "*.txt")
	if err != nil {
		return "", errors.New("could not open target list")
	}
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("could not read target list")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("target list must be a regular file")
	}
	if info.Size() > targets.MaxListBytes {
		return "", errors.New("target list exceeds the 64 KiB limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, targets.MaxListBytes+1))
	if err != nil {
		return "", errors.New("could not read target list")
	}
	if len(b) > targets.MaxListBytes {
		return "", errors.New("target list exceeds the 64 KiB limit")
	}
	return targets.NormalizeList(string(b))
}
