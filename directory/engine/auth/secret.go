package auth

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// maxSecretFile bounds the protected RADIUS secret file.
const maxSecretFile = 64 * 1024

// secretEntropy is DPAPI's optional entropy, so a blob protected for another
// purpose on the same machine does not unprotect here.
var secretEntropy = []byte("ZTG Directory RADIUS shared secret v1")

// LoadRADIUSSecret reads a file written with ProtectSecret and returns the
// RADIUS shared secret (Windows only). The caller wipes the result.
func LoadRADIUSSecret(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open RADIUS secret file: %w", err)
	}
	defer f.Close()
	blob, err := io.ReadAll(io.LimitReader(f, maxSecretFile+1))
	if err != nil {
		return nil, fmt.Errorf("read RADIUS secret file: %w", err)
	}
	defer wipe(blob)
	if len(blob) == 0 || len(blob) > maxSecretFile {
		return nil, errors.New("the RADIUS secret file is empty or too large")
	}
	secret, err := UnprotectSecret(blob)
	if err != nil {
		return nil, fmt.Errorf("unprotect RADIUS secret %s: %w", path, err)
	}
	if len(secret) == 0 {
		return nil, errors.New("the RADIUS shared secret is empty")
	}
	return secret, nil
}
