package rcj

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Encoding names reported for an imported file.
const (
	UTF16LE     = "UTF-16LE (byte order mark)"
	UTF16BE     = "UTF-16BE (byte order mark)"
	UTF8BOM     = "UTF-8 (byte order mark)"
	UTF8        = "UTF-8"
	Windows1252 = "Windows-1252 (assumed ANSI)"
)

// cp1252High maps 0x80 to 0x9F. Zero marks the five undefined bytes.
var cp1252High = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

// Decode turns the bytes of a job file into text. Files without a byte order
// mark are UTF-8 when valid and otherwise ANSI, which is read as Windows-1252
// (the code page is not stored in the file, so the caller must say so).
func Decode(data []byte) (string, string, error) {
	if len(data) > MaxBytes {
		return "", "", fmt.Errorf("job file exceeds %d bytes", MaxBytes)
	}
	var text, encoding string
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		t, err := decodeUTF16(data[2:], false)
		if err != nil {
			return "", "", err
		}
		text, encoding = t, UTF16LE
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		t, err := decodeUTF16(data[2:], true)
		if err != nil {
			return "", "", err
		}
		text, encoding = t, UTF16BE
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		if !utf8.Valid(data[3:]) {
			return "", "", errors.New("file has a UTF-8 byte order mark but is not valid UTF-8")
		}
		text, encoding = string(data[3:]), UTF8BOM
	case utf8.Valid(data):
		text, encoding = string(data), UTF8
	default:
		var b strings.Builder
		for _, c := range data {
			switch {
			case c < 0x80:
				b.WriteByte(c)
			case c >= 0xA0:
				b.WriteRune(rune(c))
			default:
				r := cp1252High[c-0x80]
				if r == 0 {
					return "", "", fmt.Errorf("byte 0x%02X is not defined in Windows-1252; save the file as UTF-8 or UTF-16", c)
				}
				b.WriteRune(r)
			}
		}
		text, encoding = b.String(), Windows1252
	}
	for _, r := range text {
		if r == 0 {
			return "", "", errors.New("file contains NUL characters; it is not a text job file (UTF-16 needs a byte order mark)")
		}
		if r == utf8.RuneError || r == 0x7F || r < 32 && r != '\t' && r != '\r' && r != '\n' {
			return "", "", errors.New("file contains control or invalid characters")
		}
	}
	return text, encoding, nil
}

func decodeUTF16(data []byte, bigEndian bool) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("UTF-16 file has an odd number of bytes")
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		if bigEndian {
			units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
		} else {
			units[i] = uint16(data[2*i+1])<<8 | uint16(data[2*i])
		}
	}
	runes := utf16.Decode(units)
	for _, r := range runes {
		if r == utf8.RuneError {
			return "", errors.New("UTF-16 file contains an unpaired surrogate")
		}
	}
	return string(runes), nil
}

// Encode returns the bytes to write for a job file. Mode "auto" writes plain
// ASCII bytes (identical in ANSI and UTF-8, so no byte order mark can upset the
// engine) and switches to UTF-16LE with a byte order mark only for text that
// is not ASCII. "utf8" writes UTF-8 without a mark; "utf16" always writes
// UTF-16LE with a mark.
func Encode(text, mode string) ([]byte, error) {
	ascii := true
	for i := 0; i < len(text); i++ {
		if text[i] >= 0x80 {
			ascii = false
		}
	}
	switch mode {
	case "", "auto":
		if ascii {
			return []byte(text), nil
		}
		fallthrough
	case "utf16":
		units := utf16.Encode([]rune(text))
		out := make([]byte, 0, 2+2*len(units))
		out = append(out, 0xFF, 0xFE)
		for _, u := range units {
			out = append(out, byte(u), byte(u>>8))
		}
		return out, nil
	case "utf8":
		return []byte(text), nil
	}
	return nil, fmt.Errorf("unknown encoding %q; use auto, utf8 or utf16", mode)
}
