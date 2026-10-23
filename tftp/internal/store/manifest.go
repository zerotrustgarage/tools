package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// LoadManifest accepts a small, explicit map from canonical remote names to SHA-256.
func LoadManifest(name string) (map[string]string, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 1<<20 {
		return nil, errors.New("manifest exceeds 1 MiB")
	}
	var doc struct {
		Files map[string]string `json:"files"`
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&doc); e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing manifest data")
	}
	if len(doc.Files) == 0 || len(doc.Files) > 4096 {
		return nil, errors.New("manifest must list 1..4096 files")
	}
	for n, h := range doc.Files {
		canonical, e := Name(n)
		if e != nil || canonical != n {
			return nil, errors.New("manifest names must be canonical portable relative paths")
		}
		digest, e := hex.DecodeString(h)
		if e != nil || len(digest) != 32 {
			return nil, errors.New("invalid manifest SHA-256")
		}
		doc.Files[n] = strings.ToLower(h)
	}
	return doc.Files, nil
}

// Approve enables immutable verified snapshots. The operator-controlled served tree
// is read once per approved name; peers only receive a copy matching its digest.
func (s *Store) Approve(files map[string]string) error {
	dir, e := os.MkdirTemp("", "ztg-tftp-approved-")
	if e != nil {
		return e
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		os.RemoveAll(dir)
		return e
	}
	s.approved = files
	s.snapshotLock = make(chan struct{}, 1)
	s.snapshotDir = dir
	s.snapshots = r
	s.cached = map[string]string{}
	return nil
}
func (s *Store) approvedRead(ctx context.Context, name string) (*os.File, error) {
	n, e := Name(name)
	if e != nil {
		return nil, e
	}
	expected, ok := s.approved[n]
	if !ok {
		return nil, errors.New("image is not approved")
	}
	select {
	case s.snapshotLock <- struct{}{}:
		defer func() { <-s.snapshotLock }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if cached, ok := s.cached[n]; ok {
		return s.snapshots.Open(cached)
	}
	f, e := s.readOriginal(n)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	id := randomID()
	out, e := s.snapshots.OpenFile(id, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			out.Close()
			s.snapshots.Remove(id)
		}
	}()
	h := sha256.New()
	if _, e = io.Copy(io.MultiWriter(out, h), contextReader{ctx, f}); e != nil {
		return nil, e
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return nil, errors.New("image digest does not match approval")
	}
	if _, e = out.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	s.cached[n] = id
	success = true
	return out, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
