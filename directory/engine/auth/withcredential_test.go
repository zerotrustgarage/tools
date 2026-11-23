package auth

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

func TestWithCredential(t *testing.T) {
	sess, _, _, _ := signedIn(t)
	defer sess.Close()

	var seen []byte
	err := sess.WithCredential(func(c *ad.Credential) error {
		if c.User != "a-alex" || c.Domain != "EXAMPLE" || string(c.Password) != goodPassword {
			t.Fatalf("credential %q %q %q", c.User, c.Domain, c.Password)
		}
		seen = c.Password
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(seen, make([]byte, len(seen))) {
		t.Fatal("revealed password not wiped after use")
	}
	if sessionPassword(t, sess) != goodPassword {
		t.Fatal("WithCredential changed the session credential")
	}

	// fn's error is returned, and the password is wiped even when fn swaps it.
	boom := errors.New("boom")
	err = sess.WithCredential(func(c *ad.Credential) error {
		seen = c.Password
		c.Password = []byte("other")
		return boom
	})
	if !errors.Is(err, boom) || !bytes.Equal(seen, make([]byte, len(seen))) {
		t.Fatalf("error %v, or password not wiped", err)
	}
	if err := sess.WithCredential(nil); err == nil {
		t.Fatal("nil fn accepted")
	}
}

func TestWithCredentialRefuses(t *testing.T) {
	called := func(t *testing.T) func(*ad.Credential) error {
		return func(*ad.Credential) error { t.Fatal("fn called"); return nil }
	}

	sess, _, _, _ := signedIn(t)
	sess.Lock()
	if err := sess.WithCredential(called(t)); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked: %v", err)
	}
	sess.Close()
	if err := sess.WithCredential(called(t)); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed: %v", err)
	}

	sess, _, _, clk := signedIn(t)
	defer sess.Close()
	clk.advance(9 * time.Hour)
	if err := sess.WithCredential(called(t)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}
