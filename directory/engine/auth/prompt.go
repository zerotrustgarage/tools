package auth

import (
	"errors"
	"io"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrUnsupportedPlatform reports a Windows-only function called elsewhere.
var ErrUnsupportedPlatform = errors.New("this sign-in function is supported only on Windows")

// ErrNotConsole reports that ReadPassword was given something other than an
// interactive console, such as a pipe or a file. Credentials are never read
// from files or redirected input (design 6.5 step 2).
var ErrNotConsole = errors.New("the password can be read only from an interactive console")

// ErrCancelled reports that the user cancelled the credential prompt.
var ErrCancelled = errors.New("sign-in was cancelled")

// ErrSmartCardNotSupportedYet reports a smart card or other certificate
// credential from the Windows prompt (runbook R122, not yet built).
var ErrSmartCardNotSupportedYet = errors.New("smart card sign-in is not supported yet; sign in with a user name and password")

// maxSecretLine bounds a typed password, in bytes.
const maxSecretLine = 1024

// readSecretLine reads one line into a buffer that is never reallocated, so
// no partial copy is left behind; on error the buffer is wiped.
func readSecretLine(r io.Reader) ([]byte, error) {
	buf := make([]byte, 0, maxSecretLine)
	var one [1]byte
	for {
		n, err := r.Read(one[:])
		if n == 1 {
			switch c := one[0]; c {
			case '\n':
				one[0] = 0
				return buf, nil
			case '\r':
			default:
				if len(buf) == cap(buf) {
					one[0] = 0
					wipe(buf)
					return nil, errors.New("the password is too long")
				}
				buf = append(buf, c)
			}
			one[0] = 0
			continue
		}
		if err == io.EOF {
			if len(buf) == 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return buf, nil
		}
		if err != nil {
			wipe(buf)
			return nil, err
		}
	}
}

// utf16ToUTF8 converts a NUL-terminated UTF-16 buffer to UTF-8 bytes without
// an intermediate string, so the result can be wiped.
func utf16ToUTF8(s []uint16) []byte {
	n := 0
	for n < len(s) && s[n] != 0 {
		n++
	}
	s = s[:n]
	out := make([]byte, 0, 3*n)
	for i := 0; i < len(s); i++ {
		r := rune(s[i])
		if utf16.IsSurrogate(r) && i+1 < len(s) {
			if d := utf16.DecodeRune(r, rune(s[i+1])); d != utf8.RuneError {
				r = d
				i++
			}
		}
		if utf16.IsSurrogate(r) {
			r = utf8.RuneError
		}
		out = utf8.AppendRune(out, r)
	}
	return out
}
