package metrics

import (
	"errors"
	"io"
	"os"
	"unicode/utf16"
)

// ErrNotUnicode means the log does not start with a UTF-16LE byte order mark.
var ErrNotUnicode = errors.New("native log does not start with a UTF-16LE byte order mark")

const (
	tailChunk = 1 << 20
	// tailBudget bounds the work of one poll; a backlog is read on the next ones.
	tailBudget = 32 << 20
)

// Tailer reads a native Unicode log (/UNILOG) while Robocopy writes it. It keeps
// an offset, decodes UTF-16LE across arbitrary read boundaries (odd bytes and
// split surrogate pairs) and starts again if the file is replaced by a shorter
// one. A file that does not exist yet is not an error.
//
// Whether the engine writes the log promptly while it runs is not validated on
// real Windows yet (rc1.md); when it does not, only the I/O signal moves live.
type Tailer struct {
	path    string
	offset  int64
	bom     bool
	odd     []byte
	high    uint16
	failed  error
	buf     []byte
	scratch []uint16
}

func NewTailer(path string) *Tailer { return &Tailer{path: path} }

// Poll passes every newly written, decoded piece to emit and returns the first
// error that makes the log unreadable. After ErrNotUnicode it reads nothing more.
func (t *Tailer) Poll(emit func(string)) error {
	if t.failed != nil {
		return t.failed
	}
	f, err := os.Open(t.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < t.offset {
		t.offset, t.bom, t.odd, t.high = 0, false, nil, 0
	}
	if t.buf == nil {
		t.buf = make([]byte, tailChunk)
	}
	for budget := tailBudget; budget > 0; budget -= tailChunk {
		n, err := f.ReadAt(t.buf, t.offset)
		if n > 0 {
			t.offset += int64(n)
			if err := t.decode(t.buf[:n], emit); err != nil {
				t.failed = err
				return err
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (t *Tailer) decode(data []byte, emit func(string)) error {
	if len(t.odd) > 0 {
		data = append(append([]byte(nil), t.odd...), data...)
		t.odd = nil
	}
	if !t.bom {
		if len(data) < 2 {
			t.odd = append([]byte(nil), data...)
			return nil
		}
		if data[0] != 0xff || data[1] != 0xfe {
			return ErrNotUnicode
		}
		t.bom = true
		data = data[2:]
	}
	if len(data)%2 == 1 {
		t.odd = []byte{data[len(data)-1]}
		data = data[:len(data)-1]
	}
	units := t.scratch[:0]
	if t.high != 0 {
		units = append(units, t.high)
		t.high = 0
	}
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
	}
	if n := len(units); n > 0 && units[n-1] >= 0xd800 && units[n-1] <= 0xdbff {
		t.high = units[n-1]
		units = units[:n-1]
	}
	t.scratch = units
	if len(units) > 0 {
		emit(string(utf16.Decode(units)))
	}
	return nil
}
