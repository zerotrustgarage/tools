//go:build !windows

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsOnlyFunctions(t *testing.T) {
	if _, err := ReadPassword("Password: ", os.Stdin, nil); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("ReadPassword: %v", err)
	}
	if _, err := PromptWindowsCredential("ZTG Directory", "Sign in"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("PromptWindowsCredential: %v", err)
	}
	if _, err := ProtectSecret([]byte("s")); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("ProtectSecret: %v", err)
	}
	path := filepath.Join(t.TempDir(), "radius.secret")
	os.WriteFile(path, []byte("blob"), 0o600)
	if _, err := LoadRADIUSSecret(path); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("LoadRADIUSSecret: %v", err)
	}
}
