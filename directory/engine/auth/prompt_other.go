//go:build !windows

package auth

import (
	"io"
	"os"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// ReadPassword is supported only on Windows, where console echo can be
// turned off with SetConsoleMode.
func ReadPassword(string, *os.File, io.Writer) ([]byte, error) { return nil, ErrUnsupportedPlatform }

// PromptWindowsCredential is supported only on Windows.
func PromptWindowsCredential(string, string) (*ad.Credential, error) {
	return nil, ErrUnsupportedPlatform
}
