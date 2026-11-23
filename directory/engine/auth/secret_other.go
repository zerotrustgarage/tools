//go:build !windows

package auth

// ProtectSecret is supported only on Windows (DPAPI).
func ProtectSecret([]byte) ([]byte, error) { return nil, ErrUnsupportedPlatform }

// UnprotectSecret is supported only on Windows (DPAPI).
func UnprotectSecret([]byte) ([]byte, error) { return nil, ErrUnsupportedPlatform }
