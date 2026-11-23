//go:build !windows

package ad

import "context"

// Open requires Windows because signed and sealed Negotiate LDAP is provided
// by the Windows LDAP client. Portable callers can use FakeClient. On Windows
// an empty Server or BaseDN is resolved through the DC locator and the
// rootDSE, so neither is checked here.
func Open(context.Context, Config) (Client, error) { return nil, ErrUnsupportedPlatform }

// OpenWriter is available only on Windows.
func OpenWriter(context.Context, Config) (Writer, error) { return nil, ErrUnsupportedPlatform }
