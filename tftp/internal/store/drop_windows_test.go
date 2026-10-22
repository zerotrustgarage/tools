package store

import (
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDropPermissions(t *testing.T) {
	d, _, _ := newDrop(t, 4096)
	r := commit(t, d, "router.cfg", "password 0 SECRET\n")
	token, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{".", ".incoming", ".index", r.StoredAs} {
		f, e := d.root.Open(name)
		if e != nil {
			t.Fatal(e)
		}
		sd, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		owner, _, e := sd.Owner()
		if e != nil || !owner.Equals(token.User.Sid) {
			t.Fatal("unexpected file owner", name, e)
		}
		dacl, _, e := sd.DACL()
		if e != nil || dacl == nil || dacl.AceCount != 1 {
			t.Fatal("unexpected DACL", name, e)
		}
		text := sd.String()
		if text == "" {
			t.Fatal("empty security descriptor")
		}
	}
	u, e := d.Begin(netip.MustParseAddr("127.0.0.1"), "empty", 0)
	if e != nil {
		t.Fatal("drop not writable after ACL enforcement", e)
	}
	u.Close()
}
