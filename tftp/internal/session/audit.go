package session

import (
	"io"
	"os"
	"sync"
)

type lockedWriter struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	return w.w.Write(b)
}

func (w *lockedWriter) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if c, ok := w.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
