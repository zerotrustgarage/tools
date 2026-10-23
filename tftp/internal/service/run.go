package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/session"
	"io"
	"os"
	"sync"
)

type running struct {
	server  *server.Server
	file    *os.File
	forward *Forward
	done    chan error
	cancel  context.CancelFunc
}

func start(ctx context.Context, c Config) (*running, error) {
	v, e := c.Server()
	if e != nil {
		return nil, e
	}
	log, e := session.AuditFile(c.Root, c.AuditDir)
	if e != nil {
		return nil, e
	}
	f, e := NewForward(c)
	if e != nil {
		log.Close()
		return nil, e
	}
	child, cancel := context.WithCancel(ctx)
	writer := &fanout{local: log, remote: f, cancel: cancel}
	v.Audit = writer
	s, e := server.StartService(child, v)
	if e != nil {
		cancel()
		log.Close()
		f.Close()
		return nil, e
	}
	r := &running{server: s, file: log, forward: f, done: make(chan error, 1), cancel: cancel}
	go func() {
		e := s.Wait()
		if e == nil {
			e = writer.Err()
		}
		r.done <- e
	}()
	return r, nil
}
func (r *running) stop() error {
	r.cancel()
	r.server.Stop()
	e := <-r.done
	ce := r.file.Close()
	r.forward.Close()
	if e != nil {
		return e
	}
	return ce
}

// Run validates changes before stopping listeners and restores the previous
// configuration if binding the new one fails. Each generation has its own audit.
func Run(ctx context.Context, name string, reload <-chan struct{}, status io.Writer) error {
	current, e := Load(name)
	if e != nil {
		return e
	}
	active, e := start(ctx, current)
	if e != nil {
		return e
	}
	requestedReload := reload
	for {
		select {
		case <-ctx.Done():
			return active.stop()
		case e := <-active.done:
			active.cancel()
			active.file.Close()
			active.forward.Close()
			return e
		case _, ok := <-requestedReload:
			if !ok {
				requestedReload = nil
				continue
			}
			next, e := Load(name)
			if e != nil {
				fmt.Fprintln(status, "Reload rejected:", e)
				continue
			}
			if e = active.stop(); e != nil {
				return e
			}
			active, e = start(ctx, next)
			if e != nil {
				fmt.Fprintln(status, "Reload failed; restoring prior configuration:", e)
				active, e = start(ctx, current)
				if e != nil {
					return errors.New("reload and rollback failed; service stopped")
				}
				continue
			}
			current = next
			fmt.Fprintln(status, "Configuration reloaded")
		}
	}
}

// fanout cancels on forwarding failure but continues recording shutdown outcomes
// locally. A broken remote sink must not poison the local audit encoder.
type fanout struct {
	local, remote io.Writer
	cancel        context.CancelFunc
	mu            sync.Mutex
	err           error
}

func (w *fanout) Write(b []byte) (int, error) {
	n, e := w.local.Write(b)
	if e != nil || n != len(b) {
		if e == nil {
			e = io.ErrShortWrite
		}
		w.cancel()
		return n, e
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		n, e = w.remote.Write(b)
		if e == nil && n != len(b) {
			e = io.ErrShortWrite
		}
		if e != nil {
			w.err = e
			w.cancel()
		}
	}
	return len(b), nil
}
func (w *fanout) Err() error { w.mu.Lock(); defer w.mu.Unlock(); return w.err }
