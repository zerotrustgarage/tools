package private

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	advapi = syscall.NewLazyDLL("advapi32.dll")
	convertSD = advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	setSecurity = advapi.NewProc("SetFileSecurityW")
	localFree = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

func protect(name string, directory bool) error {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sddl, err := syscall.UTF16PtrFromString("D:P(A;" + inherit + ";FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	var sd uintptr
	ok, _, callErr := convertSD.Call(uintptr(unsafe.Pointer(sddl)), 1, uintptr(unsafe.Pointer(&sd)), 0)
	if ok == 0 {
		return fmt.Errorf("create owner-only descriptor: %w", callErr)
	}
	defer localFree.Call(sd)
	path, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ok, _, callErr = setSecurity.Call(uintptr(unsafe.Pointer(path)), 0x80000004, sd)
	if ok == 0 {
		return fmt.Errorf("set protected owner-only DACL: %w", callErr)
	}
	return nil
}
