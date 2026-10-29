//go:build windows

package probe

import "golang.org/x/sys/windows"

// setTTLOne sets the IPv4 TTL of a socket to 1, as RFC 4795 section 2.5
// requires of a TCP LLMNR query, so the query cannot leave the local link.
func setTTLOne(fd uintptr) error {
	return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windows.IP_TTL, 1)
}
