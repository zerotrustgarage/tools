package cmdline

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRenderAgainstNativeWindowsTokenizer(t *testing.T) {
	args := []string{"robocopy.exe", `C:\source space\`, `\\nas\share\`, `C:\`, "", `a\"b`, "日本語-😀", "/MT:8"}
	command, err := Render(args)
	if err != nil {
		t.Fatal(err)
	}
	got, err := windows.DecomposeCommandLine(command)
	if err != nil || !reflect.DeepEqual(args, got) {
		t.Fatalf("native round trip: %q -> %q (%v)", args, got, err)
	}
}
