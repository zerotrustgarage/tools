//go:build !linux && !windows

package probe

import (
	"context"
	"time"
)

// Where the IPv6 neighbor table is not read yet (macOS), IPv6 neighbor
// discovery reports itself unavailable and the scan is IPv4 only.
func newNeighborTable6() (NeighborTable6, error) { return nil, ErrUnavailable }

func multicastPing6(context.Context, int, string, time.Duration) error { return ErrUnavailable }
