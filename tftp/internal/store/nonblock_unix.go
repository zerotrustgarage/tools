//go:build !windows

package store

import "syscall"

const nonblock = syscall.O_NONBLOCK
