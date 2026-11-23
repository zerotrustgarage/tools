//go:build !windows

package ad

import (
	"context"
	"fmt"
)

// Open requires Windows because signed and sealed Negotiate LDAP is provided
// by the Windows LDAP client. Portable callers can use FakeClient.
func Open(_ context.Context, cfg Config) (Client, error) {
	if cfg.Server == "" || cfg.BaseDN == "" {
		return nil, fmt.Errorf("server and base DN are required")
	}
	return nil, ErrUnsupportedPlatform
}

// OpenWriter is available only on Windows.
func OpenWriter(context.Context, Config) (Writer, error) { return nil, ErrUnsupportedPlatform }
