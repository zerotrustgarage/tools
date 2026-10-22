package cmdline

import (
	"reflect"
	"syscall"
	"testing"
	"unsafe"
)

func TestRenderAgainstNativeWindowsTokenizer(t *testing.T) {
	args := []string{"robocopy.exe", `C:\source space\`, `\\nas\share\`, `C:\`, "", `a\"b`, "日本語-😀", "/MT:8"}
	command, err := Render(args)
	if err != nil {
		t.Fatal(err)
	}
	native, err := syscall.UTF16PtrFromString(command)
	if err != nil {
		t.Fatal(err)
	}
	var count int32
	ptr, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("CommandLineToArgvW").Call(uintptr(unsafe.Pointer(native)), uintptr(unsafe.Pointer(&count)))
	if ptr == 0 {
		t.Fatal(callErr)
	}
	defer syscall.LocalFree(syscall.Handle(ptr))
	if count < 0 || count > 100 {
		t.Fatal("invalid native argument count")
	}
	pointers := unsafe.Slice((**uint16)(unsafe.Pointer(ptr)), int(count))
	got := make([]string, len(pointers))
	for i, p := range pointers {
		// Each pointer belongs to the allocated native command-line buffer.
		n := 0
		for *(*uint16)(unsafe.Add(unsafe.Pointer(p), n*2)) != 0 {
			n++
		}
		got[i] = syscall.UTF16ToString(unsafe.Slice(p, n))
	}
	if !reflect.DeepEqual(args, got) {
		t.Fatalf("native round trip: %q -> %q", args, got)
	}
}
