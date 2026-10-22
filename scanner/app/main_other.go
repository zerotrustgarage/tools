//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The desktop app ships for Windows first (docs/design.md, slice S2). On
// other platforms the CLI covers the same scans.
func main() {
	fmt.Fprintln(os.Stderr, "The ZTG Scanner desktop app currently runs on Windows. Use the ztg-scanner CLI on this platform.")
	os.Exit(1)
}
