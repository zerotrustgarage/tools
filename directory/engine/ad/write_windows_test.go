//go:build windows

package ad

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// These tests run WinLDAP's real write encoders against the loopback fixture
// in response_windows_test.go. They never bind with Negotiate or credentials.

type recordedWrites struct {
	mu       sync.Mutex
	requests []ldapWriteRequest
}

func (r *recordedWrites) add(req ldapWriteRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, ldapWriteRequest{tag: req.tag, operation: append([]byte(nil), req.operation...), message: append([]byte(nil), req.message...)})
}

func (r *recordedWrites) all() []ldapWriteRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ldapWriteRequest(nil), r.requests...)
}

func noSearch(ldapSearchRequest) ([][]byte, int) { return nil, 0 }

func TestWinLDAPWritesMapResultCodes(t *testing.T) {
	codes := map[byte]int{0x66: 50, 0x68: 68, 0x4a: 32, 0x6c: 19}
	fixture := startLDAPFixtureWithWrites(t, noSearch, func(req ldapWriteRequest) int { return codes[req.tag] })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dn := "CN=Alex,OU=People,DC=example,DC=test"
	if err := fixture.client.Modify(ctx, dn, []Mod{{Op: ModReplace, Name: "title", Values: []string{"Engineer"}}}); !errors.Is(err, ErrInsufficientRights) {
		t.Errorf("Modify: %v, want ErrInsufficientRights", err)
	}
	if err := fixture.client.Add(ctx, dn, []Attribute{{Name: "objectClass", Values: []string{"user"}}}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Add: %v, want ErrAlreadyExists", err)
	}
	if err := fixture.client.Delete(ctx, dn); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, want ErrNotFound", err)
	}
	if err := fixture.client.Rename(ctx, dn, "CN=Alex Smith", ""); !errors.Is(err, ErrConstraint) {
		t.Errorf("Rename: %v, want ErrConstraint", err)
	}
}

func TestWinLDAPModifyEncodesOperations(t *testing.T) {
	var rec recordedWrites
	fixture := startLDAPFixtureWithWrites(t, noSearch, func(req ldapWriteRequest) int { rec.add(req); return 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dn := "CN=Zoë,OU=People,DC=example,DC=test"
	err := fixture.client.Modify(ctx, dn, []Mod{
		{Op: ModReplace, Name: "title", Values: []string{"Ingénieure"}},
		{Op: ModDelete, Name: "description"},
		{Op: ModAdd, Name: "proxyAddresses", Values: []string{"smtp:a@example.test", "smtp:b@example.test"}},
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].tag != 0x66 {
		t.Fatalf("requests = %d, want one ModifyRequest", len(reqs))
	}
	type change struct {
		op     int64
		name   string
		values []string
	}
	outer, err := berChildren(reqs[0].operation)
	if err != nil || len(outer) != 1 {
		t.Fatalf("ModifyRequest envelope: %v", err)
	}
	fields, err := berChildren(outer[0].value)
	if err != nil || len(fields) != 2 || string(fields[0].value) != dn {
		t.Fatalf("ModifyRequest object: %q %v", fields, err)
	}
	items, err := berChildren(fields[1].value)
	if err != nil {
		t.Fatal(err)
	}
	var got []change
	for _, item := range items {
		parts, err := berChildren(item.value)
		if err != nil || len(parts) != 2 || parts[0].tag != 0x0a {
			t.Fatalf("change: %v", err)
		}
		partial, err := berChildren(parts[1].value)
		if err != nil || len(partial) != 2 {
			t.Fatalf("partial attribute: %v", err)
		}
		vals, err := berChildren(partial[1].value)
		if err != nil {
			t.Fatal(err)
		}
		c := change{op: decodeBERInteger(parts[0].value), name: string(partial[0].value)}
		for _, v := range vals {
			c.values = append(c.values, string(v.value))
		}
		got = append(got, c)
	}
	want := []change{{2, "title", []string{"Ingénieure"}}, {1, "description", nil}, {0, "proxyAddresses", []string{"smtp:a@example.test", "smtp:b@example.test"}}}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].op != want[i].op || got[i].name != want[i].name || !equalStrings(got[i].values, want[i].values) {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWinLDAPSetPasswordRefusesUnsealedConnection(t *testing.T) {
	var rec recordedWrites
	fixture := startLDAPFixtureWithWrites(t, noSearch, func(req ldapWriteRequest) int { rec.add(req); return 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The loopback client is neither sealed nor LDAPS.
	if err := fixture.client.SetPassword(ctx, "CN=Alex,DC=example,DC=test", []byte("N3w-Passw0rd")); !errors.Is(err, errNotSealed) {
		t.Fatalf("SetPassword: %v, want errNotSealed", err)
	}
	if err := fixture.client.Modify(ctx, "CN=Alex,DC=example,DC=test", []Mod{{Op: ModReplace, Name: "unicodePwd", Values: []string{"x"}}}); err == nil {
		t.Fatal("Modify wrote unicodePwd")
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d write requests reached the server", n)
	}
}

func TestWinLDAPSetPasswordSendsQuotedUTF16(t *testing.T) {
	var rec recordedWrites
	fixture := startLDAPFixtureWithWrites(t, noSearch, func(req ldapWriteRequest) int { rec.add(req); return 19 })
	// Simulate a sealed session; the fixture itself is plain loopback.
	fixture.client.sealed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := fixture.client.SetPassword(ctx, "CN=Alex,DC=example,DC=test", []byte("Pä55!"))
	if !errors.Is(err, ErrConstraint) {
		t.Fatalf("SetPassword: %v, want ErrConstraint", err)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].tag != 0x66 {
		t.Fatalf("requests = %d, want one ModifyRequest", len(reqs))
	}
	want, _ := unicodePwdValue([]byte("Pä55!"))
	if !bytes.Contains(reqs[0].operation, want) || !bytes.Contains(reqs[0].operation, []byte("unicodePwd")) {
		t.Fatal("ModifyRequest does not carry the quoted UTF-16LE unicodePwd value")
	}
	// operation 2 (replace) precedes the attribute.
	if !bytes.Contains(reqs[0].operation, []byte{0x0a, 0x01, 0x02}) {
		t.Fatal("unicodePwd is not replaced")
	}
}

func TestWinLDAPUndeleteSendsShowDeletedControl(t *testing.T) {
	var rec recordedWrites
	fixture := startLDAPFixtureWithWrites(t, noSearch, func(req ldapWriteRequest) int { rec.add(req); return 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deleted := `CN=Alex\0ADEL:6f9619ff-8b86-d011-b42d-00c04fc964ff,CN=Deleted Objects,DC=example,DC=test`
	if err := fixture.client.Undelete(ctx, deleted, "CN=Alex,OU=People,DC=example,DC=test"); err != nil {
		t.Fatalf("Undelete: %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].tag != 0x66 {
		t.Fatalf("requests = %d, want one ModifyRequest", len(reqs))
	}
	for _, want := range []string{ldapServerShowDeletedOID, "isDeleted", "distinguishedName", "CN=Alex,OU=People,DC=example,DC=test"} {
		if !bytes.Contains(reqs[0].message, []byte(want)) {
			t.Errorf("undelete request lacks %q", want)
		}
	}
}

func TestWinLDAPRenameMovesAndDeletesOldRDN(t *testing.T) {
	var rec recordedWrites
	fixture := startLDAPFixtureWithWrites(t, noSearch, func(req ldapWriteRequest) int { rec.add(req); return 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fixture.client.Rename(ctx, "CN=Alex,OU=People,DC=example,DC=test", "CN=Alex Smith", "OU=Leavers,DC=example,DC=test"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].tag != 0x6c {
		t.Fatalf("requests = %d, want one ModifyDNRequest", len(reqs))
	}
	outer, err := berChildren(reqs[0].operation)
	if err != nil || len(outer) != 1 {
		t.Fatal("ModifyDNRequest envelope")
	}
	fields, err := berChildren(outer[0].value)
	if err != nil || len(fields) != 4 {
		t.Fatalf("ModifyDNRequest has %d fields, want 4 (%v)", len(fields), err)
	}
	if string(fields[1].value) != "CN=Alex Smith" || fields[2].tag != 0x01 || len(fields[2].value) != 1 || fields[2].value[0] == 0 ||
		fields[3].tag != 0x80 || string(fields[3].value) != "OU=Leavers,DC=example,DC=test" {
		t.Fatalf("ModifyDNRequest fields = %q", fields)
	}
}
