//go:build !windows

package store

import "os"

func checkReadable(*os.File) error { return nil }
