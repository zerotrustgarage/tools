package server

import (
	"github.com/zerotrustgarage/tools/tftp/internal/audit"
	"io"
	"time"
)

type progress struct {
	server      *Server
	record      audit.Record
	last, start time.Time
}

func (p *progress) add(n int) {
	p.record.Bytes += int64(n)
	if p.server.config.OnProgress == nil {
		return
	}
	now := time.Now()
	if !p.last.IsZero() && now.Sub(p.last) < 250*time.Millisecond {
		return
	}
	p.last = now
	p.record.Result = "active"
	p.record.Time = now.UTC()
	p.record.DurationMS = now.Sub(p.start).Milliseconds()
	p.server.config.OnProgress(p.record)
}

type progressReader struct {
	io.Reader
	p progress
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, e := r.Reader.Read(b)
	r.p.add(n)
	return n, e
}

type progressWriter struct {
	io.Writer
	p progress
}

func (w *progressWriter) Write(b []byte) (int, error) {
	n, e := w.Writer.Write(b)
	w.p.add(n)
	return n, e
}
func (s *Server) progressReader(r io.Reader, a audit.Record) io.Reader {
	return &progressReader{r, progress{server: s, record: a, start: time.Now()}}
}
func (s *Server) progressWriter(w io.Writer, a audit.Record) io.Writer {
	return &progressWriter{w, progress{server: s, record: a, start: time.Now()}}
}
