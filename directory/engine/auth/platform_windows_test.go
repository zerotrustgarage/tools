//go:build windows

package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	plain := []byte("radius shared secret \x00 with binary")
	blob, err := ProtectSecret(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("radius shared secret")) {
		t.Fatal("blob contains the plain text")
	}
	got, err := UnprotectSecret(blob)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "radius.secret")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err = LoadRADIUSSecret(path); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("load: %q %v", got, err)
	}
	blob[len(blob)-1] ^= 1
	if _, err := UnprotectSecret(blob); err == nil {
		t.Fatal("tampered blob unprotected")
	}
}

func TestReadPasswordRefusesRedirectedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password.txt")
	os.WriteFile(path, []byte("secret\n"), 0o600)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := ReadPassword("Password: ", f, nil); !errors.Is(err, ErrNotConsole) {
		t.Fatalf("got %v", err)
	}
}
