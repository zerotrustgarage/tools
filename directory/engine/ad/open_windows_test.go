//go:build windows

package ad

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"unsafe"
)

func TestOpenRejectsMissingSettingsBeforeBinding(t *testing.T) {
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("Open accepted an empty config")
	}
}

// Loads only the system DLL and resolves the read-only entry points. It never
// opens a session or contacts a domain, so this also runs on hosted Windows CI.
func TestSystemLDAPEntryPoints(t *testing.T) {
	if err := loadSystemLDAP(); err != nil {
		t.Fatal(err)
	}
	for _, proc := range []*syscall.LazyProc{ldapInit, ldapSetOption, ldapBind,
		ldapConnect, ldapSearch, ldapFirstEntry, ldapNextEntry, ldapGetDN,
		ldapGetValues, ldapGetValuesLen, ldapValuesFree, ldapValuesFreeLen,
		ldapMemFree, ldapMsgFree, ldapUnbind, ldapFirstAttribute, ldapNextAttribute, berFree} {
		if err := proc.Find(); err != nil {
			t.Errorf("resolve %s: %v", proc.Name, err)
		}
	}
	if ldapBind.Name != "ldap_bind_sW" {
		t.Fatal("Negotiate requires synchronous binding")
	}
	var timeval ldapTimeval
	var berval ldapBerval
	if unsafe.Sizeof(timeval) != 8 || unsafe.Sizeof(berval.Length) != 4 || unsafe.Offsetof(berval.Value) != unsafe.Sizeof(uintptr(0)) {
		t.Fatal("WinLDAP ULONG/timeval/BERVAL layout does not match the Windows ABI")
	}
}

func TestUTF16ResponseLimitCannotSilentlyTruncate(t *testing.T) {
	units := make([]uint16, 32769)
	for i := 0; i < 32768; i++ {
		units[i] = 'a'
	}
	if _, err := utf16FromPtr(&units[0]); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected incomplete long string, got %v", err)
	}
	short, _ := syscall.UTF16FromString("Zoë 東京")
	if got, err := utf16FromPtr(&short[0]); err != nil || got != "Zoë 東京" {
		t.Fatalf("Unicode decode: %q %v", got, err)
	}
}
