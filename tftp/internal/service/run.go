package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/session"
	"io"
	"os"
)

type running struct {
	server  *server.Server
	file    *os.File
	forward *Forward
	done    chan error
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
	v.Audit = io.MultiWriter(log, f)
	s, e := server.StartService(ctx, v)
	if e != nil {
		log.Close()
		f.Close()
		return nil, e
	}
	r := &running{s, log, f, make(chan error, 1)}
	go func() { r.done <- s.Wait() }()
	return r, nil
}
func (r *running) stop() error {
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
	for {
		select {
		case <-ctx.Done():
			return active.stop()
		case e := <-active.done:
			active.file.Close()
			active.forward.Close()
			return e
		case <-reload:
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
