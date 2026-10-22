package store

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

var reopenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

func lockDrop(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

func private(f *os.File, directory bool) error {
	token, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	descriptor, e := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + token.User.Sid.String() + ")")
	if e != nil {
		return e
	}
	dacl, _, e := descriptor.DACL()
	if e != nil {
		return e
	}
	// Reopen the handle, rather than resolving a path outside os.Root. The
	// original read handle lacks WRITE_DAC. ReOpenFile preserves object identity.
	h, _, callErr := reopenFile.Call(f.Fd(), windows.WRITE_DAC|windows.WRITE_OWNER, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if windows.Handle(h) == windows.InvalidHandle {
		return fmt.Errorf("reopen permission handle: %w", callErr)
	}
	defer windows.CloseHandle(windows.Handle(h))
	e = windows.SetSecurityInfo(windows.Handle(h), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, token.User.Sid, nil, dacl, nil)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(f)
	if e != nil {
		return fmt.Errorf("apply private DACL: %w", e)
	}
	return nil
}
func freeBytes(f *os.File) (uint64, error) {
	buf := make([]uint16, 32768)
	n, e := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
	if e != nil {
		return 0, e
	}
	if n >= uint32(len(buf)) {
		return 0, errors.New("drop box path too long")
	}
	name, e := windows.UTF16PtrFromString(windows.UTF16ToString(buf[:n]) + "\\")
	if e != nil {
		return 0, e
	}
	var available uint64
	e = windows.GetDiskFreeSpaceEx(name, &available, nil, nil)
	runtime.KeepAlive(f)
	return available, e
}
