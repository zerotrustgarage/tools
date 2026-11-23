//go:build windows

package auth

import (
	"fmt"
	"unsafe"
)

// cryptProtectMemorySameProcess (CRYPTPROTECTMEMORY_SAME_PROCESS): only this
// process can decrypt the buffer.
const cryptProtectMemorySameProcess = 0

func protectMemory(b []byte) error   { return memoryCall(b, true) }
func unprotectMemory(b []byte) error { return memoryCall(b, false) }

func memoryCall(b []byte, protect bool) error {
	if len(b) == 0 || len(b)%protectBlock != 0 {
		return fmt.Errorf("memory protection needs whole %d-byte blocks", protectBlock)
	}
	a, err := loadAPI()
	if err != nil {
		return err
	}
	proc, name := a.cryptProtectMemory, "CryptProtectMemory"
	if !protect {
		proc, name = a.cryptUnprotectMemory, "CryptUnprotectMemory"
	}
	if err := find(proc); err != nil {
		return err
	}
	r, _, callErr := proc.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), cryptProtectMemorySameProcess)
	if r == 0 {
		return fmt.Errorf("%s failed: %v", name, callErr)
	}
	return nil
}
