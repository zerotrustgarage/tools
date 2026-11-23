//go:build windows

package ad

import (
	"testing"
	"unsafe"
)

type fieldLayout struct {
	name   string
	offset uintptr
	want   uintptr
}

func checkLayout(t *testing.T, typ string, size, wantSize uintptr, fields []fieldLayout) {
	t.Helper()
	if size != wantSize {
		t.Errorf("%s: size %d, want %d", typ, size, wantSize)
	}
	for _, f := range fields {
		if f.offset != f.want {
			t.Errorf("%s.%s: offset %d, want %d", typ, f.name, f.offset, f.want)
		}
	}
}

// The structures passed to wldap32, secur32 and netapi32 must match the C
// layouts in winldap.h, sspi.h and dsgetdc.h. The expected offsets are the
// natural C layout on 64-bit Windows (amd64 and arm64 share it): ULONG is 4
// bytes, pointers and ULONG_PTR are 8, BOOLEAN is 1, GUID is 16 with 4-byte
// alignment.
func TestNativeStructLayouts64(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layouts")
	}
	var mod ldapMod
	checkLayout(t, "LDAPModW", unsafe.Sizeof(mod), 24, []fieldLayout{
		{"mod_op", unsafe.Offsetof(mod.Op), 0},
		{"mod_type", unsafe.Offsetof(mod.Type), 8},
		{"mod_vals", unsafe.Offsetof(mod.Values), 16},
	})
	var bv ldapBerval
	checkLayout(t, "berval", unsafe.Sizeof(bv), 16, []fieldLayout{
		{"bv_len", unsafe.Offsetof(bv.Length), 0},
		{"bv_val", unsafe.Offsetof(bv.Value), 8},
	})
	var ctl ldapControl
	checkLayout(t, "LDAPControlW", unsafe.Sizeof(ctl), 32, []fieldLayout{
		{"ldctl_oid", unsafe.Offsetof(ctl.OID), 0},
		{"ldctl_value", unsafe.Offsetof(ctl.Value), 8},
		{"ldctl_iscritical", unsafe.Offsetof(ctl.IsCritical), 24},
	})
	var id secWinntAuthIdentityEx
	checkLayout(t, "SEC_WINNT_AUTH_IDENTITY_EXW", unsafe.Sizeof(id), 72, []fieldLayout{
		{"Version", unsafe.Offsetof(id.Version), 0},
		{"Length", unsafe.Offsetof(id.Length), 4},
		{"User", unsafe.Offsetof(id.User), 8},
		{"UserLength", unsafe.Offsetof(id.UserLength), 16},
		{"Domain", unsafe.Offsetof(id.Domain), 24},
		{"DomainLength", unsafe.Offsetof(id.DomainLength), 32},
		{"Password", unsafe.Offsetof(id.Password), 40},
		{"PasswordLength", unsafe.Offsetof(id.PasswordLength), 48},
		{"Flags", unsafe.Offsetof(id.Flags), 52},
		{"PackageList", unsafe.Offsetof(id.PackageList), 56},
		{"PackageListLength", unsafe.Offsetof(id.PackageListLength), 64},
	})
	var h secHandle
	checkLayout(t, "CtxtHandle", unsafe.Sizeof(h), 16, []fieldLayout{
		{"dwLower", unsafe.Offsetof(h.Lower), 0},
		{"dwUpper", unsafe.Offsetof(h.Upper), 8},
	})
	var neg secPkgContextNegotiationInfo
	checkLayout(t, "SecPkgContext_NegotiationInfoW", unsafe.Sizeof(neg), 16, []fieldLayout{
		{"PackageInfo", unsafe.Offsetof(neg.PackageInfo), 0},
		{"NegotiationState", unsafe.Offsetof(neg.NegotiationState), 8},
	})
	var pkg secPkgInfo
	checkLayout(t, "SecPkgInfoW", unsafe.Sizeof(pkg), 32, []fieldLayout{
		{"fCapabilities", unsafe.Offsetof(pkg.Capabilities), 0},
		{"wVersion", unsafe.Offsetof(pkg.Version), 4},
		{"wRPCID", unsafe.Offsetof(pkg.RPCID), 6},
		{"cbMaxToken", unsafe.Offsetof(pkg.MaxToken), 8},
		{"Name", unsafe.Offsetof(pkg.Name), 16},
		{"Comment", unsafe.Offsetof(pkg.Comment), 24},
	})
	var dci domainControllerInfo
	checkLayout(t, "DOMAIN_CONTROLLER_INFOW", unsafe.Sizeof(dci), 80, []fieldLayout{
		{"DomainControllerName", unsafe.Offsetof(dci.DomainControllerName), 0},
		{"DomainControllerAddress", unsafe.Offsetof(dci.DomainControllerAddress), 8},
		{"DomainControllerAddressType", unsafe.Offsetof(dci.DomainControllerAddressType), 16},
		{"DomainGuid", unsafe.Offsetof(dci.DomainGUID), 20},
		{"DomainName", unsafe.Offsetof(dci.DomainName), 40},
		{"DnsForestName", unsafe.Offsetof(dci.DNSForestName), 48},
		{"Flags", unsafe.Offsetof(dci.Flags), 56},
		{"DcSiteName", unsafe.Offsetof(dci.DCSiteName), 64},
		{"ClientSiteName", unsafe.Offsetof(dci.ClientSiteName), 72},
	})
	if unsafe.Sizeof(guid{}) != 16 || unsafe.Alignof(guid{}) != 4 {
		t.Errorf("GUID size/alignment %d/%d, want 16/4", unsafe.Sizeof(guid{}), unsafe.Alignof(guid{}))
	}
}

// Resolves the SSPI and DC locator entry points from the system directory
// without calling them, so it never contacts a domain.
func TestSystemSecurityAndLocatorEntryPoints(t *testing.T) {
	if err := loadSecur32(); err != nil {
		t.Fatal(err)
	}
	if err := loadNetapi32(); err != nil {
		t.Fatal(err)
	}
	for _, proc := range []interface {
		Find() error
	}{queryContextAttributes, freeContextBuffer, getUserNameEx, dsGetDcName, netAPIBufFree} {
		if err := proc.Find(); err != nil {
			t.Error(err)
		}
	}
	if currentUserName() == "" {
		t.Error("GetUserNameExW returned no name for the current identity")
	}
}
