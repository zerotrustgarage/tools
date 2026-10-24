package engine

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

type fakeSys struct {
	version    string
	versionErr error
	elev       ElevationInfo
	elevErr    error
	privs      map[string]bool
	privErr    error
	embedded   uint32
	embeddedEr error
	cat        CatalogResult
	catErr     error
	catCalled  bool
}

func (f *fakeSys) FileVersion(string) (string, error)   { return f.version, f.versionErr }
func (f *fakeSys) Elevation() (ElevationInfo, error)    { return f.elev, f.elevErr }
func (f *fakeSys) Privileges() (map[string]bool, error) { return f.privs, f.privErr }
func (f *fakeSys) EmbeddedTrust(string) (uint32, error) { return f.embedded, f.embeddedEr }
func (f *fakeSys) CatalogTrust(string) (CatalogResult, error) {
	f.catCalled = true
	return f.cat, f.catErr
}

func TestInspectPopulatesEveryField(t *testing.T) {
	f := &fakeSys{version: "10.0.26100.1", elev: ElevationInfo{Type: 2, Elevated: true},
		privs: map[string]bool{"SeBackupPrivilege": true, "SeRestorePrivilege": false}}
	m := InspectWith(`C:\Windows\System32\Robocopy.exe`, f)
	if m.FileVersion.Value != "10.0.26100.1" || m.FileVersion.Source == "" || m.FileVersion.Unavailable != "" {
		t.Fatalf("version: %+v", m.FileVersion)
	}
	if m.Elevation.Type != ElevationFull || m.Elevation.Elevated == nil || !*m.Elevation.Elevated || m.Elevation.Source == "" {
		t.Fatalf("elevation: %+v", m.Elevation)
	}
	want := []Privilege{{"SeBackupPrivilege", PrivilegeEnabled}, {"SeRestorePrivilege", PrivilegeDisabled}, {"SeSecurityPrivilege", PrivilegeAbsent}}
	if len(m.Privileges.Items) != 3 {
		t.Fatalf("privileges: %+v", m.Privileges)
	}
	for i, p := range want {
		if m.Privileges.Items[i] != p {
			t.Fatalf("privilege %d: %+v want %+v", i, m.Privileges.Items[i], p)
		}
	}
}

func TestInspectElevationTypes(t *testing.T) {
	for typ, want := range map[uint32]string{1: ElevationDefault, 2: ElevationFull, 3: ElevationLimited, 9: ElevationUnknown} {
		m := InspectWith("x", &fakeSys{elev: ElevationInfo{Type: typ}, embedded: trustNoSignature})
		if m.Elevation.Type != want {
			t.Fatalf("type %d: %+v", typ, m.Elevation)
		}
		if typ == 9 && m.Elevation.Unavailable == "" {
			t.Fatal("unrecognized type must say why")
		}
	}
}

func TestSignatureDecisions(t *testing.T) {
	cases := []struct {
		name        string
		f           fakeSys
		decision    string
		catCalled   bool
		detail      string
		unavailable bool
	}{
		{"embedded", fakeSys{embedded: 0}, SignatureEmbedded, false, "", false},
		{"catalog", fakeSys{embedded: trustNoSignature, cat: CatalogResult{Found: true, Catalog: "x.cat"}}, SignatureCatalog, true, "x.cat", false},
		{"unsigned", fakeSys{embedded: trustNoSignature}, SignatureUnsigned, true, "no catalog", false},
		{"provider unknown", fakeSys{embedded: trustProviderUnknown}, SignatureUnknown, true, "unsupported subject format", false},
		{"subject form unknown", fakeSys{embedded: trustSubjectFormUnk}, SignatureUnknown, true, "unsupported subject format", false},
		{"bad embedded", fakeSys{embedded: 0x80096010}, SignatureUnknown, false, "0x80096010", false},
		{"catalog fails", fakeSys{embedded: trustNoSignature, cat: CatalogResult{Found: true, Catalog: "x.cat", Status: 0x80096010}}, SignatureUnknown, true, "x.cat", false},
		{"embedded api error", fakeSys{embeddedEr: errors.New("boom")}, SignatureUnknown, false, "", true},
		{"catalog api error", fakeSys{embedded: trustNoSignature, catErr: errors.New("boom")}, SignatureUnknown, true, "", true},
		{"non-windows", fakeSys{embeddedEr: ErrUnsupported}, SignatureUnknown, false, "", true},
	}
	for _, c := range cases {
		f := c.f
		m := InspectWith("x", &f)
		s := m.Signature
		if s.Decision != c.decision || f.catCalled != c.catCalled || !strings.Contains(s.Detail, c.detail) || (s.Unavailable != "") != c.unavailable || s.Source == "" {
			t.Errorf("%s: %+v catalog=%v", c.name, s, f.catCalled)
		}
	}
}

func TestInspectRecordsWhyUnavailable(t *testing.T) {
	boom := errors.New("access denied")
	m := InspectWith("x", &fakeSys{versionErr: boom, elevErr: boom, privErr: boom, embeddedEr: boom})
	if !strings.Contains(m.FileVersion.Unavailable, "access denied") || m.FileVersion.Value != "" {
		t.Fatalf("version: %+v", m.FileVersion)
	}
	if m.Elevation.Type != ElevationUnknown || m.Elevation.Elevated != nil || m.Elevation.Unavailable == "" {
		t.Fatalf("elevation: %+v", m.Elevation)
	}
	if len(m.Privileges.Items) != 0 || m.Privileges.Unavailable == "" {
		t.Fatalf("privileges: %+v", m.Privileges)
	}
	m = InspectWith("x", &fakeSys{version: ""})
	if m.FileVersion.Unavailable == "" {
		t.Fatal("missing version resource must be explained")
	}
}

func TestInspectOnUnsupportedPlatform(t *testing.T) {
	m := InspectWith("x", unsupportedPlatform{})
	for _, reason := range []string{m.FileVersion.Unavailable, m.Elevation.Unavailable, m.Privileges.Unavailable, m.Signature.Unavailable} {
		if !strings.Contains(reason, "non-Windows") {
			t.Fatalf("reason %q", reason)
		}
	}
}

type unsupportedPlatform struct{}

func (unsupportedPlatform) FileVersion(string) (string, error)   { return "", ErrUnsupported }
func (unsupportedPlatform) Elevation() (ElevationInfo, error)    { return ElevationInfo{}, ErrUnsupported }
func (unsupportedPlatform) Privileges() (map[string]bool, error) { return nil, ErrUnsupported }
func (unsupportedPlatform) EmbeddedTrust(string) (uint32, error) { return 0, ErrUnsupported }
func (unsupportedPlatform) CatalogTrust(string) (CatalogResult, error) {
	return CatalogResult{}, ErrUnsupported
}

func TestParseFixedFileInfo(t *testing.T) {
	// VS_VERSIONINFO header (6 bytes) + "VS_VERSION_INFO\0" in UTF-16 (32), padded to 40, then the fixed info.
	data := make([]byte, 40+52)
	binary.LittleEndian.PutUint16(data[0:], uint16(len(data)))
	binary.LittleEndian.PutUint32(data[40:], 0xFEEF04BD)
	binary.LittleEndian.PutUint32(data[48:], 10<<16|0)
	binary.LittleEndian.PutUint32(data[52:], 26100<<16|7309)
	got, err := parseFixedFileInfo(data)
	if err != nil || got != "10.0.26100.7309" {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := parseFixedFileInfo(make([]byte, 100)); err == nil {
		t.Fatal("accepted data without the signature")
	}
	if _, err := parseFixedFileInfo(nil); err == nil {
		t.Fatal("accepted empty data")
	}
}
