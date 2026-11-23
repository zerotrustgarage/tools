//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The desktop console runs on Windows (design section 4). The UI test server
// in uitest/server serves the same frontend against the demo directory on any
// platform.
func main() {
	fmt.Fprintln(os.Stderr, "The ZTG Directory desktop app runs on Windows. Use the ztg-directory CLI on this platform.")
	os.Exit(1)
}
