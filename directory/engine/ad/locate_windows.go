//go:build windows

package ad

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// DsGetDcNameW flags (dsgetdc.h).
// https://learn.microsoft.com/windows/win32/api/dsgetdc/nf-dsgetdc-dsgetdcnamew
const (
	dsDirectoryServiceRequired = 0x00000010
	dsWritableRequired         = 0x00001000
	dsTryNextClosestSite       = 0x00040000
	dsReturnDNSName            = 0x40000000

	locatorFlags = dsDirectoryServiceRequired | dsWritableRequired | dsReturnDNSName | dsTryNextClosestSite
)

// guid mirrors GUID.
type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// domainControllerInfo mirrors DOMAIN_CONTROLLER_INFOW.
type domainControllerInfo struct {
	DomainControllerName        *uint16
	DomainControllerAddress     *uint16
	DomainControllerAddressType uint32
	DomainGUID                  guid
	DomainName                  *uint16
	DNSForestName               *uint16
	Flags                       uint32
	DCSiteName                  *uint16
	ClientSiteName              *uint16
}

var (
	netapi32                   *syscall.LazyDLL
	dsGetDcName, netAPIBufFree *syscall.LazyProc
	netapi32Once               sync.Once
	netapi32Err                error
)

func loadNetapi32() error {
	netapi32Once.Do(func() {
		dir, err := systemDirectory()
		if err != nil {
			netapi32Err = err
			return
		}
		netapi32 = syscall.NewLazyDLL(dir + `\netapi32.dll`)
		dsGetDcName = netapi32.NewProc("DsGetDcNameW")
		netAPIBufFree = netapi32.NewProc("NetApiBufferFree")
		netapi32Err = netapi32.Load()
	})
	return netapi32Err
}

// locateDC asks the DC locator for a writable domain controller of domain
// (the computer's own domain when empty), in the caller's site or the next
// closest one, and returns its DNS host name (runbook R119).
func locateDC(domain string) (string, error) {
	if err := loadNetapi32(); err != nil {
		return "", fmt.Errorf("load the DC locator from the system directory: %w", err)
	}
	var domainp *uint16
	if d := strings.TrimSpace(domain); d != "" {
		p, err := syscall.UTF16PtrFromString(d)
		if err != nil {
			return "", err
		}
		domainp = p
	}
	var info uintptr
	r, _, _ := dsGetDcName.Call(0, uintptr(unsafe.Pointer(domainp)), 0, 0, locatorFlags, uintptr(unsafe.Pointer(&info)))
	if r != 0 {
		target := domain
		if target == "" {
			target = "this computer's domain"
		}
		return "", fmt.Errorf("find a writable domain controller for %s (DsGetDcNameW): %w", target, syscall.Errno(r))
	}
	if info == 0 {
		return "", errors.New("DsGetDcNameW returned no domain controller")
	}
	defer netAPIBufFree.Call(info)
	dci := (*domainControllerInfo)(nativePointer(info))
	name, err := utf16FromPtr(dci.DomainControllerName)
	if err != nil {
		return "", err
	}
	name = trimDCName(name)
	if name == "" {
		return "", errors.New("DsGetDcNameW returned an empty domain controller name")
	}
	return name, nil
}
