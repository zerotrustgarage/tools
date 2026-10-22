package xfer

import (
	"bufio"
	"errors"
	"io"
)

type asciiReader struct {
	r          *bufio.Reader
	pending    byte
	hasPending bool
}

func (r *asciiReader) Read(b []byte) (int, error) {
	for i := range b {
		if r.hasPending {
			b[i] = r.pending
			r.hasPending = false
			continue
		}
		c, e := r.r.ReadByte()
		if e != nil {
			return i, e
		}
		switch c {
		case '\n':
			b[i] = '\r'
			r.pending = '\n'
			r.hasPending = true
		case '\r':
			b[i] = '\r'
			r.pending = 0
			r.hasPending = true
		default:
			b[i] = c
		}
	}
	return len(b), nil
}

type asciiWriter struct {
	w  io.Writer
	cr bool
}

func (w *asciiWriter) Write(b []byte) (int, error) {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if w.cr {
			switch c {
			case '\n':
				out = append(out, '\n')
			case 0:
				out = append(out, '\r')
			default:
				return 0, errors.New("invalid netascii escape")
			}
			w.cr = false
		} else if c == '\r' {
			w.cr = true
		} else if c == '\n' {
			return 0, errors.New("unescaped netascii newline")
		} else {
			out = append(out, c)
		}
	}
	n, e := w.w.Write(out)
	if e == nil && n != len(out) {
		e = io.ErrShortWrite
	}
	if e != nil {
		return 0, e
	}
	return len(b), nil
}
