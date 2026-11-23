//go:build windows

package ad

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"unsafe"
)

// An empty Config is valid on Windows (DC locator and rootDSE), so these
// configurations are rejected before the locator or any network call.
func TestOpenRejectsInvalidSettingsBeforeBinding(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		ctx context.Context
		cfg Config
	}{
		"negative timeout": {context.Background(), Config{Timeout: -1}},
		"empty user":       {context.Background(), Config{Credential: &Credential{Password: []byte("x")}}},
		"empty password":   {context.Background(), Config{Credential: &Credential{User: "alex", Domain: "EXAMPLE"}}},
		"canceled context": {canceled, Config{}},
		"writer, no user":  {context.Background(), Config{Credential: &Credential{}}},
		"writer, canceled": {canceled, Config{Server: "dc.invalid"}},
	} {
		open := Open
		if name == "writer, no user" || name == "writer, canceled" {
			open = func(ctx context.Context, cfg Config) (Client, error) { return OpenWriter(ctx, cfg) }
		}
		if c, err := open(tc.ctx, tc.cfg); err == nil || c != nil {
			t.Errorf("%s: Open accepted the config", name)
		}
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
		ldapMemFree, ldapMsgFree, ldapUnbind, ldapFirstAttribute, ldapNextAttribute, berFree,
		ldapGetOption, ldapAdd, ldapModify, ldapDelete, ldapRename} {
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
