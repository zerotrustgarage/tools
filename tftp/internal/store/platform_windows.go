package store

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var reopenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

func lockDrop(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

func private(f *os.File, directory bool) error {
	defer runtime.KeepAlive(f)
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
	// ReOpenFile rejects directory handles. Reopen directories through an empty
	// NT name relative to the already contained handle, as os.Root does for '.'.
	// Neither branch resolves a pathname outside the runtime Root.
	var h windows.Handle
	if directory {
		name, err := windows.NewNTUnicodeString("")
		if err != nil {
			return err
		}
		attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(f.Fd()), ObjectName: name, Attributes: windows.OBJ_DONT_REPARSE}
		attrs.Length = uint32(unsafe.Sizeof(attrs))
		err = windows.NtCreateFile(&h, windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER|windows.SYNCHRONIZE, &attrs, &windows.IO_STATUS_BLOCK{}, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
		if err != nil {
			return fmt.Errorf("reopen directory permission handle: %w", err)
		}
	} else {
		value, _, callErr := reopenFile.Call(f.Fd(), windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0)
		if windows.Handle(value) == windows.InvalidHandle {
			return fmt.Errorf("reopen file permission handle: %w", callErr)
		}
		h = windows.Handle(value)
	}
	defer windows.CloseHandle(h)
	e = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, token.User.Sid, nil, dacl, nil)
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
