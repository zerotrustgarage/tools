package cmdline

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWindowsArguments(t *testing.T) {
	args := []string{"robocopy.exe", `C:\source space\`, `\\server\share\`, `C:\`, "café-日本語-😀", `a\"b`, "", "/MT:8"}
	command, err := Render(args)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Tokenize(command)
	if err != nil || !reflect.DeepEqual(args, got) {
		t.Fatalf("%q: %q, %v", command, got, err)
	}
	got, err = Tokenize(`robocopy "C:\source space" D:\backup /XD "C:\source space\cache" /R:2`)
	if err != nil || len(got) != 7 || got[1] != `C:\source space` || got[5] != `C:\source space\cache` {
		t.Fatalf("%q, %v", got, err)
	}
	for _, bad := range []string{"a\x00b", "a\nb", `"unclosed`} {
		if _, err := Tokenize(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func FuzzRoundTrip(f *testing.F) {
	f.Add(`["robocopy","C:\\data space\\","\\\\nas\\share\\","/MIR"]`)
	f.Add(`["","a\\\\\"b","日本語😀","/XF","*.tmp"]`)
	f.Fuzz(func(t *testing.T, input string) {
		var args []string
		if json.Unmarshal([]byte(input), &args) != nil || len(args) == 0 {
			return
		}
		command, err := Render(args)
		if err != nil {
			return
		}
		got, err := Tokenize(command)
		if err != nil || !reflect.DeepEqual(args, got) {
			t.Fatalf("round trip: input %q, command %q, got %q (%v)", args, command, got, err)
		}
	})
}
