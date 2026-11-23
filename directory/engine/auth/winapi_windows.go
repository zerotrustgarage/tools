//go:build windows

package auth

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// winAPI holds the Windows functions this package calls. Every DLL is loaded
// by absolute path from the system directory, never by a bare name that the
// loader would search for (as loadSystemLDAP in engine/ad).
type winAPI struct {
	getConsoleMode, setConsoleMode, localFree       *syscall.LazyProc
	credUIPrompt, credUnPack                        *syscall.LazyProc
	coTaskMemFree                                   *syscall.LazyProc
	credIsMarshaled                                 *syscall.LazyProc
	lsaConnectUntrusted, lsaLookupPackage, lsaClose *syscall.LazyProc
	cryptProtectData, cryptUnprotectData            *syscall.LazyProc
	cryptProtectMemory, cryptUnprotectMemory        *syscall.LazyProc
}

var (
	apiOnce sync.Once
	api     winAPI
	apiErr  error
)

func loadAPI() (*winAPI, error) {
	apiOnce.Do(func() {
		// kernel32 is a KnownDLL; it reports the trusted system directory.
		getSystemDirectory := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemDirectoryW")
		buf := make([]uint16, 32768)
		n, _, callErr := getSystemDirectory.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || n >= uintptr(len(buf)) {
			apiErr = fmt.Errorf("GetSystemDirectoryW failed: %v", callErr)
			return
		}
		dir := syscall.UTF16ToString(buf[:n]) + `\`
		kernel32 := syscall.NewLazyDLL(dir + "kernel32.dll")
		credui := syscall.NewLazyDLL(dir + "credui.dll")
		ole32 := syscall.NewLazyDLL(dir + "ole32.dll")
		advapi32 := syscall.NewLazyDLL(dir + "advapi32.dll")
		secur32 := syscall.NewLazyDLL(dir + "secur32.dll")
		crypt32 := syscall.NewLazyDLL(dir + "crypt32.dll")
		api = winAPI{
			getConsoleMode:       kernel32.NewProc("GetConsoleMode"),
			setConsoleMode:       kernel32.NewProc("SetConsoleMode"),
			localFree:            kernel32.NewProc("LocalFree"),
			credUIPrompt:         credui.NewProc("CredUIPromptForWindowsCredentialsW"),
			credUnPack:           credui.NewProc("CredUnPackAuthenticationBufferW"),
			coTaskMemFree:        ole32.NewProc("CoTaskMemFree"),
			credIsMarshaled:      advapi32.NewProc("CredIsMarshaledCredentialW"),
			lsaConnectUntrusted:  secur32.NewProc("LsaConnectUntrusted"),
			lsaLookupPackage:     secur32.NewProc("LsaLookupAuthenticationPackage"),
			lsaClose:             secur32.NewProc("LsaDeregisterLogonProcess"),
			cryptProtectData:     crypt32.NewProc("CryptProtectData"),
			cryptUnprotectData:   crypt32.NewProc("CryptUnprotectData"),
			cryptProtectMemory:   crypt32.NewProc("CryptProtectMemory"),
			cryptUnprotectMemory: crypt32.NewProc("CryptUnprotectMemory"),
		}
	})
	if apiErr != nil {
		return nil, apiErr
	}
	return &api, nil
}

// find resolves procs before first use so a missing DLL or export is an
// error, not a panic inside Call.
func find(procs ...*syscall.LazyProc) error {
	for _, p := range procs {
		if err := p.Find(); err != nil {
			return fmt.Errorf("load %s from the system directory: %w", p.Name, err)
		}
	}
	return nil
}
