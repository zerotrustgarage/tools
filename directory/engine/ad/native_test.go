package ad

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLDAPResultErrorMapsCodes(t *testing.T) {
	cases := []struct {
		code uint32
		want error
	}{
		{32, ErrNotFound}, {49, ErrInvalidCredentials}, {50, ErrInsufficientRights}, {68, ErrAlreadyExists},
		{19, ErrConstraint}, {20, ErrConstraint}, {21, ErrConstraint}, {53, ErrConstraint},
		{64, ErrConstraint}, {65, ErrConstraint}, {66, ErrConstraint}, {67, ErrConstraint},
	}
	for _, tc := range cases {
		err := ldapResultError("modify", tc.code, "0000052D: SvcErr: DSID-031A1254, problem 5003 (WILL_NOT_PERFORM), data 0\n\x00")
		if !errors.Is(err, tc.want) {
			t.Errorf("code %d: %v, want errors.Is(%v)", tc.code, err, tc.want)
		}
		if !strings.Contains(err.Error(), "0000052D: SvcErr") || strings.ContainsAny(err.Error(), "\n\x00") {
			t.Errorf("code %d: server text missing or not cleaned: %q", tc.code, err.Error())
		}
	}
	for _, code := range []uint32{1, 51, 52, 80, 0x51, 0x55, 999} {
		err := ldapResultError("add", code, "")
		for _, sentinel := range []error{ErrNotFound, ErrInvalidCredentials, ErrInsufficientRights, ErrAlreadyExists, ErrConstraint} {
			if errors.Is(err, sentinel) {
				t.Errorf("code %d unexpectedly maps to %v", code, sentinel)
			}
		}
		if !strings.Contains(err.Error(), "add failed") {
			t.Errorf("code %d: %q does not name the operation", code, err)
		}
	}
	if got := ldapResultError("bind", 49, "").Error(); strings.HasSuffix(got, ": ") {
		t.Errorf("empty server message leaves a dangling separator: %q", got)
	}
}

func TestCleanServerMessageBoundsLength(t *testing.T) {
	if got := cleanServerMessage(strings.Repeat("x", 5000)); len(got) > 520 {
		t.Fatalf("server message not bounded: %d bytes", len(got))
	}
	if got := cleanServerMessage("  a\tb\r\nc\x00 "); got != "a b c" {
		t.Fatalf("cleanServerMessage = %q", got)
	}
}

func TestUnicodePwdValue(t *testing.T) {
	got, err := unicodePwdValue([]byte("Pässw0rd😀"))
	if err != nil {
		t.Fatal(err)
	}
	units := utf16.Encode([]rune(`"Pässw0rd😀"`))
	var want []byte
	for _, u := range units {
		want = append(want, byte(u), byte(u>>8))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unicodePwdValue = % x, want % x", got, want)
	}
	for _, bad := range [][]byte{nil, {}, {0xff, 0xfe}, []byte("a\x00b")} {
		if _, err := unicodePwdValue(bad); err == nil {
			t.Errorf("unicodePwdValue(%q) accepted an invalid password", bad)
		}
	}
}

func TestUTF16SecretIsTerminatedAndWipeable(t *testing.T) {
	got, err := utf16Secret([]byte("Zoë😀"))
	if err != nil {
		t.Fatal(err)
	}
	want := append(utf16.Encode([]rune("Zoë😀")), 0)
	if len(got) != len(want) || cap(got) != len(want) {
		t.Fatalf("len/cap = %d/%d, want %d exactly", len(got), cap(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("utf16Secret = %v, want %v", got, want)
		}
	}
	wipeUint16(got)
	for _, u := range got {
		if u != 0 {
			t.Fatal("wipeUint16 left data")
		}
	}
	b := []byte("secret")
	wipeBytes(b)
	if !bytes.Equal(b, make([]byte, 6)) {
		t.Fatal("wipeBytes left data")
	}
}

func TestSearchTarget(t *testing.T) {
	const base = "DC=example,DC=test"
	cases := []struct {
		req       SearchRequest
		wantBase  string
		wantScope int
		wantErr   bool
	}{
		{SearchRequest{}, base, scopeWholeSubtree, false},
		{SearchRequest{OneLevel: true}, base, scopeWholeSubtree, false},
		{SearchRequest{Under: "OU=People,DC=example,DC=test"}, "OU=People,DC=example,DC=test", scopeWholeSubtree, false},
		{SearchRequest{Under: "ou=people,dc=EXAMPLE,dc=test", OneLevel: true}, "ou=people,dc=EXAMPLE,dc=test", scopeSingleLevel, false},
		{SearchRequest{Under: base}, base, scopeWholeSubtree, false},
		{SearchRequest{Under: "OU=People,DC=other,DC=test"}, "", 0, true},
		{SearchRequest{Under: "DC=test"}, "", 0, true},
		{SearchRequest{Under: "OU=Evil,DC=notexample,DC=test"}, "", 0, true},
	}
	for _, tc := range cases {
		gotBase, gotScope, err := searchTarget(tc.req, base)
		if (err != nil) != tc.wantErr || gotBase != tc.wantBase || gotScope != tc.wantScope {
			t.Errorf("searchTarget(%+v) = %q, %d, %v", tc.req, gotBase, gotScope, err)
		}
	}
}

func TestDeletedFilter(t *testing.T) {
	got, err := deletedFilter(SearchRequest{})
	if err != nil || got != "(isDeleted=TRUE)" {
		t.Fatalf("empty: %q %v", got, err)
	}
	got, err = deletedFilter(SearchRequest{Query: "a*", Class: ClassUser, Under: "OU=X,DC=example", OneLevel: true})
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := BuildFilter(SearchRequest{Query: "a*", Class: ClassUser})
	if got != "(&(isDeleted=TRUE)"+inner+")" || !strings.Contains(got, `a\2a`) {
		t.Fatalf("deletedFilter = %q", got)
	}
	if !balancedSingle(got) {
		t.Fatalf("unbalanced filter %q", got)
	}
	if _, err := deletedFilter(SearchRequest{Class: "printer"}); err == nil {
		t.Fatal("unknown class accepted")
	}
	if got := deletedObjectsDN("DC=example,DC=test"); got != "CN=Deleted Objects,DC=example,DC=test" {
		t.Fatalf("deletedObjectsDN = %q", got)
	}
}

func TestTrimDCNameAndGUIDText(t *testing.T) {
	if got := trimDCName(`\\dc1.example.test`); got != "dc1.example.test" {
		t.Fatalf("trimDCName = %q", got)
	}
	g := []byte{0xff, 0x19, 0x96, 0x6f, 0x86, 0x8b, 0x11, 0xd0, 0xb4, 0x2d, 0x00, 0xc0, 0x4f, 0xc9, 0x64, 0xff}
	if got := guidText(g); got != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Fatalf("guidText = %q", got)
	}
	if guidText(g[:15]) != "invalid" {
		t.Fatal("short GUID accepted")
	}
}

func TestAttributeListsNeverRequestSecrets(t *testing.T) {
	for _, list := range [][]string{safeAttrs, searchAttrs, computedAttrs, deletedAttrs, rightsAttrs} {
		seen := map[string]bool{}
		for _, a := range list {
			if secretAttribute(a) {
				t.Errorf("secret attribute %q is requested", a)
			}
			if seen[lowerASCII(a)] {
				t.Errorf("duplicate attribute %q", a)
			}
			seen[lowerASCII(a)] = true
			if strings.EqualFold(a, "mS-DS-ConsistencyGuid") || strings.EqualFold(a, "objectGUID") || strings.EqualFold(a, "objectSid") {
				t.Errorf("binary attribute %q is in a string list", a)
			}
		}
	}
	for _, s := range []string{"unicodePwd", "userPassword", "ms-Mcs-AdmPwd", "msLAPS-Password", "msLAPS-EncryptedPassword",
		"msFVE-RecoveryPassword", "supplementalCredentials", "dBCSPwd", "ntPwdHistory", "lmPwdHistory"} {
		if !secretAttribute(s) {
			t.Errorf("secretAttribute(%q) = false", s)
		}
	}
	for _, a := range []string{"member", "directReports"} {
		if !containsFold(safeAttrs, a) || containsFold(searchAttrs, a) {
			t.Errorf("%s must be read by Get only", a)
		}
	}
	if len(lowerAll([]string{"cN", "User"})) != 2 || lowerAll([]string{"cN"})[0] != "cn" {
		t.Fatal("lowerAll")
	}
	if !kerberosPackage("Kerberos") || !kerberosPackage(" kerberos ") || kerberosPackage("NTLM") || kerberosPackage("Negotiate") || kerberosPackage("") {
		t.Fatal("kerberosPackage")
	}
}
