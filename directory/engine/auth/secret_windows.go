//go:build windows

package auth

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// CryptProtectData flags (dpapi.h).
const (
	cryptprotectUIForbidden  = 0x1
	cryptprotectLocalMachine = 0x4
)

// dataBlob is DATA_BLOB.
type dataBlob struct {
	size uint32
	data *byte
}

func blobOf(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{size: uint32(len(b)), data: &b[0]}
}

// ProtectSecret encrypts plain with DPAPI for this machine
// (CRYPTPROTECT_LOCAL_MACHINE, so the service and every technician on the
// machine can read it, and CRYPTPROTECT_UI_FORBIDDEN). Protect the file
// with an ACL as well: DPAPI machine scope does not limit which local
// account can decrypt.
func ProtectSecret(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("the secret is empty")
	}
	a, err := loadAPI()
	if err != nil {
		return nil, err
	}
	if err := find(a.cryptProtectData, a.localFree); err != nil {
		return nil, err
	}
	desc, err := syscall.UTF16PtrFromString("ZTG Directory RADIUS shared secret")
	if err != nil {
		return nil, err
	}
	in, entropy := blobOf(plain), blobOf(secretEntropy)
	var out dataBlob
	r, _, callErr := a.cryptProtectData.Call(uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Pointer(desc)),
		uintptr(unsafe.Pointer(&entropy)), 0, 0, cryptprotectLocalMachine|cryptprotectUIForbidden,
		uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData failed: %v", callErr)
	}
	defer a.localFree.Call(uintptr(unsafe.Pointer(out.data)))
	return append([]byte(nil), unsafe.Slice(out.data, out.size)...), nil
}

// UnprotectSecret decrypts a blob from ProtectSecret. The caller wipes the
// result; the native copy is zeroed before LocalFree.
func UnprotectSecret(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("the protected secret is empty")
	}
	a, err := loadAPI()
	if err != nil {
		return nil, err
	}
	if err := find(a.cryptUnprotectData, a.localFree); err != nil {
		return nil, err
	}
	in, entropy := blobOf(blob), blobOf(secretEntropy)
	var out dataBlob
	r, _, callErr := a.cryptUnprotectData.Call(uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&entropy)), 0, 0, cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %v", callErr)
	}
	native := unsafe.Slice(out.data, out.size)
	plain := append([]byte(nil), native...)
	clear(native)
	a.localFree.Call(uintptr(unsafe.Pointer(out.data)))
	return plain, nil
}
