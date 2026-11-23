package ad

import (
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

// utf16Secret converts a UTF-8 secret to a NUL-terminated UTF-16 buffer
// without creating a Go string, which could not be erased. The buffer is
// allocated once at its final size, so no partial copy is left behind by
// growth; the caller erases it with wipeUint16.
func utf16Secret(secret []byte) ([]uint16, error) {
	if len(secret) == 0 {
		return nil, errors.New("the password is empty")
	}
	units := 0
	for i := 0; i < len(secret); {
		r, size := utf8.DecodeRune(secret[i:])
		if r == utf8.RuneError && size <= 1 {
			return nil, errors.New("the password is not valid UTF-8")
		}
		if r == 0 {
			return nil, errors.New("the password contains a NUL character")
		}
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		i += size
	}
	out := make([]uint16, 0, units+1)
	for i := 0; i < len(secret); {
		r, size := utf8.DecodeRune(secret[i:])
		out = utf16.AppendRune(out, r)
		i += size
	}
	return append(out, 0), nil
}

// unicodePwdValue encodes password as AD's unicodePwd value: the password
// enclosed in double quotes, as UTF-16LE without a terminator (MS-ADTS
// 3.1.1.3.1.5.1). The caller erases the result with wipeBytes.
func unicodePwdValue(password []byte) ([]byte, error) {
	units, err := utf16Secret(password)
	if err != nil {
		return nil, err
	}
	defer wipeUint16(units)
	units = units[:len(units)-1] // drop the NUL
	out := make([]byte, 0, 2*(len(units)+2))
	out = append(out, '"', 0)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	out = append(out, '"', 0)
	return out, nil
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func wipeUint16(u []uint16) {
	for i := range u {
		u[i] = 0
	}
}
