// Package session shares interactive lifecycle and audit handling between the UI and service.
package session

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/audit"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
)

type Request struct {
	Root       string `json:"root"`
	Upload     string `json:"upload"`
	Bind       string `json:"bind"`
	Port       int    `json:"port"`
	Allow      string `json:"allow"`
	Minutes    int    `json:"minutes"`
	SinglePort bool   `json:"single_port"`
	Manifest   string `json:"manifest"`
	Scanner    string `json:"scanner"`
}
type Snapshot struct {
	Running   bool           `json:"running"`
	Deadline  time.Time      `json:"deadline"`
	Addresses []string       `json:"addresses"`
	Records   []audit.Record `json:"records"`
	Active    []audit.Record `json:"active"`
	Audit     string         `json:"audit"`
	Error     string         `json:"error"`
}
type Session struct {
	writer   *lockedWriter
	mu       sync.Mutex
	op       sync.Mutex
	server   *server.Server
	log      *os.File
	request  Request
	snapshot Snapshot
	active   map[string]audit.Record
	done     chan struct{}
}

func New() *Session { return &Session{active: map[string]audit.Record{}} }

// AuditFile creates a unique log outside the served root, resolving directory aliases.
func AuditFile(root, base string) (*os.File, error) {
	if base == "" {
		base = filepath.Join(filepath.Dir(root), ".ztg-tftp-sessions")
	}
	dir := filepath.Join(base, server.ID())
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if e := store.Separate(root, dir); e != nil {
		return nil, e
	}
	return os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
}
func (s *Session) Start(ctx context.Context, r Request) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	running := s.snapshot.Running
	s.mu.Unlock()
	if running {
		return errors.New("stop the current session first")
	}
	if r.Minutes <= 0 || r.Minutes > 1440 {
		return errors.New("session length must be 1..1440 minutes")
	}
	root, e := filepath.Abs(r.Root)
	if e != nil {
		return e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	r.Root = root
	c := server.Config{Root: root, Upload: r.Upload, Bind: r.Bind, Port: r.Port, Duration: time.Duration(r.Minutes) * time.Minute, SinglePort: r.SinglePort}
	if r.Allow != "" {
		for _, v := range strings.Split(r.Allow, ",") {
			p, e := netip.ParsePrefix(strings.TrimSpace(v))
			if e != nil {
				return e
			}
			c.Allow = append(c.Allow, p.Masked())
		}
	}
	if e = applySources(&c, r.Manifest, r.Scanner); e != nil {
		return e
	}
	log, e := AuditFile(root, "")
	if e != nil {
		return e
	}
	writer := &lockedWriter{w: log}
	s.writer = writer
	c.Audit = writer
	c.OnRecord = s.record
	c.OnProgress = s.progress
	s.mu.Lock()
	s.snapshot = Snapshot{Running: true, Audit: log.Name(), Records: []audit.Record{}, Active: []audit.Record{}}
	s.active = map[string]audit.Record{}
	s.mu.Unlock()
	srv, e := server.Start(ctx, c)
	if e != nil {
		log.Close()
		s.mu.Lock()
		s.snapshot.Running = false
		s.mu.Unlock()
		return e
	}
	s.mu.Lock()
	s.server = srv
	s.log = log
	s.request = r
	s.done = make(chan struct{})
	done := s.done
	s.snapshot.Addresses = srv.Addresses()
	s.snapshot.Deadline = srv.Deadline()
	s.mu.Unlock()
	go func() {
		e := srv.Wait()
		writer.mu.Lock()
		ce := log.Close()
		writer.mu.Unlock()
		s.mu.Lock()
		if e == nil {
			e = ce
		}
		s.snapshot.Running = false
		if e != nil {
			s.snapshot.Error = e.Error()
		}
		close(done)
		s.mu.Unlock()
	}()
	return nil
}
func (s *Session) record(r audit.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, r.Transfer)
	s.snapshot.Records = append(s.snapshot.Records, r)
	if len(s.snapshot.Records) > 1000 {
		s.snapshot.Records = s.snapshot.Records[1:]
	}
}
func (s *Session) progress(r audit.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[r.Transfer] = r
}
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.snapshot
	v.Records = append([]audit.Record{}, v.Records...)
	v.Active = []audit.Record{}
	for _, r := range s.active {
		v.Active = append(v.Active, r)
	}
	v.Addresses = append([]string{}, v.Addresses...)
	return v
}
func (s *Session) Stop() {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	srv, done := s.server, s.done
	s.mu.Unlock()
	if srv != nil {
		srv.Stop()
		<-done
	}
}
func (s *Session) Extend(minutes int) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	srv := s.server
	s.mu.Unlock()
	if srv == nil {
		return errors.New("no session")
	}
	if minutes <= 0 || minutes > 1440 {
		return errors.New("extension must be 1..1440 minutes")
	}
	if e := srv.Extend(time.Duration(minutes) * time.Minute); e != nil {
		return e
	}
	s.mu.Lock()
	s.snapshot.Deadline = srv.Deadline()
	s.mu.Unlock()
	return nil
}
func (s *Session) Collected() ([]store.Receipt, error) {
	s.mu.Lock()
	dir := s.request.Upload
	s.mu.Unlock()
	if dir == "" {
		return []store.Receipt{}, nil
	}
	return store.List(dir, 1000)
}
func (s *Session) Promote(storedAs, name string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	r, log, running := s.request, s.log, s.snapshot.Running
	s.mu.Unlock()
	if !running {
		return errors.New("start a session before promoting a file")
	}
	s.writer.mu.Lock()
	defer s.writer.mu.Unlock()
	hash, e := store.Promote(r.Upload, r.Root, storedAs, name)
	if e != nil {
		return e
	}
	rec := audit.Record{Time: time.Now().UTC(), Op: "promote", Session: s.server.SessionID(), Transfer: server.ID(), Name: name, StoredAs: storedAs, SHA256: hash, SourceSHA256: hash, Result: "complete"}
	// Independent Log writers may not interleave on the same file; serialize writes
	// with the server through an explicit audit writer (set in Start).
	if e = audit.New(log).Write(rec); e != nil {
		s.server.Stop()
		return e
	}
	s.record(rec)
	return nil
}

// Export fixes the length between serialized writes so exports end on a complete JSON record.
func (s *Session) Export(w io.Writer) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	name := s.snapshot.Audit
	writer := s.writer
	s.mu.Unlock()
	if name == "" || writer == nil {
		return errors.New("no session audit")
	}
	writer.mu.Lock()
	f, e := os.Open(name)
	if e != nil {
		writer.mu.Unlock()
		return e
	}
	info, e := f.Stat()
	writer.mu.Unlock()
	defer f.Close()
	if e != nil {
		return e
	}
	_, e = io.CopyN(w, f, info.Size())
	return e
}
