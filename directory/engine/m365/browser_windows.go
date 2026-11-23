//go:build windows

package m365

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shellOnce sync.Once
	shellErr  error
	coInit    *syscall.LazyProc
	coUninit  *syscall.LazyProc
	shellExec *syscall.LazyProc
)

func loadShell() error {
	shellOnce.Do(func() {
		// kernel32 is a KnownDLL; it reports the trusted system directory, from
		// which shell32 and ole32 are then loaded by full path.
		getSystemDirectory := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemDirectoryW")
		buf := make([]uint16, 32768)
		n, _, callErr := getSystemDirectory.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || n >= uintptr(len(buf)) {
			shellErr = fmt.Errorf("GetSystemDirectoryW failed: %v", callErr)
			return
		}
		dir := syscall.UTF16ToString(buf[:n]) + `\`
		ole32 := syscall.NewLazyDLL(dir + "ole32.dll")
		shell32 := syscall.NewLazyDLL(dir + "shell32.dll")
		coInit = ole32.NewProc("CoInitializeEx")
		coUninit = ole32.NewProc("CoUninitialize")
		shellExec = shell32.NewProc("ShellExecuteW")
		for _, p := range []*syscall.LazyProc{coInit, coUninit, shellExec} {
			if err := p.Find(); err != nil {
				shellErr = err
				return
			}
		}
	})
	return shellErr
}

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	swShowNormal            = 1
	rpcEChangedMode         = 0x80010106
)

// DefaultOpenBrowser opens an https address in the default browser with
// ShellExecuteW (shell32 loaded from the system directory; never cmd.exe).
// COM is initialized on the calling thread first, as ShellExecute's
// documentation asks.
func DefaultOpenBrowser(rawURL string) error {
	if err := checkBrowserURL(rawURL); err != nil {
		return err
	}
	if err := loadShell(); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInit.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if int32(hr) >= 0 { // S_OK or S_FALSE: balance with CoUninitialize
		defer coUninit.Call()
	} else if uint32(hr) != rpcEChangedMode {
		return fmt.Errorf("CoInitializeEx failed: 0x%08x", uint32(hr))
	}
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(rawURL)
	if err != nil {
		return err
	}
	r, _, callErr := shellExec.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, swShowNormal)
	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	if r <= 32 {
		return fmt.Errorf("ShellExecuteW could not open the browser (code %d): %v", r, callErr)
	}
	return nil
}
