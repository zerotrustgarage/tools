//go:build !windows

package auth

// Without CryptProtectMemory the blob stays in clear text; the desktop app and
// live sign-in run on Windows only, so this path serves tests.
func protectMemory([]byte) error   { return nil }
func unprotectMemory([]byte) error { return nil }
