// Package record keeps ZTG Directory's change records (design 9): append-only
// JSON Lines, one file per month for each writer, each line holding the hash
// of the line before it. Records never contain passwords, LAPS values or
// recovery keys; that a password was set, by whom and when, is recorded.
package record

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kinds of entry.
const (
	KindChange       = "change"
	KindSignIn       = "sign-in"
	KindSignInFailed = "sign-in-failed"
	KindLock         = "lock"
	KindUnlock       = "unlock"
	KindSignOut      = "sign-out"
	KindRefused      = "plan-refused"
	KindTemplate     = "template"
)

// Entry is one line of the record.
type Entry struct {
	Seq        int64     `json:"seq"`
	Time       time.Time `json:"time"`
	Writer     string    `json:"writer"`
	Kind       string    `json:"kind"`
	Job        string    `json:"job,omitempty"`
	PlanHash   string    `json:"planHash,omitempty"`
	Step       int       `json:"step,omitempty"`
	Change     string    `json:"change,omitempty"`
	DN         string    `json:"dn,omitempty"`
	Attribute  string    `json:"attribute,omitempty"`
	Before     []string  `json:"before,omitempty"`
	After      []string  `json:"after,omitempty"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	Requester  string    `json:"requester,omitempty"`
	Approver   string    `json:"approver,omitempty"`
	Technician string    `json:"technician,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	DC         string    `json:"dc,omitempty"`
	Method     string    `json:"method,omitempty"`
	Device     string    `json:"device,omitempty"`
	Ticket     string    `json:"ticket,omitempty"`
	Note       string    `json:"note,omitempty"`
	Prev       string    `json:"prev"`
	Hash       string    `json:"hash"`
}

// Log appends to one writer's chain.
type Log struct {
	mu     sync.Mutex
	dir    string
	writer string
	last   string
	seq    int64
	now    func() time.Time
}

// Open opens (or starts) the chain for writer under dir. writer names the
// machine or installation; it becomes a folder name.
func Open(dir, writer string, now func() time.Time) (*Log, error) {
	writer = cleanWriter(writer)
	if writer == "" {
		return nil, errors.New("record writer name is required")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	wdir := filepath.Join(dir, writer)
	if err := os.MkdirAll(wdir, 0o750); err != nil {
		return nil, fmt.Errorf("create record folder: %w", err)
	}
	l := &Log{dir: dir, writer: writer, now: now}
	files, err := monthFiles(wdir)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		lastFile := files[len(files)-1]
		entries, err := readFile(lastFile)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			e := entries[len(entries)-1]
			l.last, l.seq = e.Hash, e.Seq
		} else if len(files) > 1 {
			prev, err := readFile(files[len(files)-2])
			if err != nil {
				return nil, err
			}
			if len(prev) > 0 {
				l.last, l.seq = prev[len(prev)-1].Hash, prev[len(prev)-1].Seq
			}
		}
	}
	return l, nil
}

// Writer returns the chain's writer name.
func (l *Log) Writer() string { return l.writer }

// Dir returns the record root folder.
func (l *Log) Dir() string { return l.dir }

// Append writes e as the next line and returns it with sequence, time, writer
// and hashes filled in. The file is synced before Append returns.
func (l *Log) Append(e Entry) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.Kind == "" {
		return Entry{}, errors.New("record entry kind is required")
	}
	e.Seq = l.seq + 1
	e.Time = l.now().UTC()
	e.Writer = l.writer
	e.Prev = l.last
	e.Hash = ""
	h, err := hashEntry(e)
	if err != nil {
		return Entry{}, err
	}
	e.Hash = h
	line, err := json.Marshal(e)
	if err != nil {
		return Entry{}, err
	}
	path := filepath.Join(l.dir, l.writer, e.Time.Format("2006-01")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return Entry{}, fmt.Errorf("open record file: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return Entry{}, fmt.Errorf("write record: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return Entry{}, fmt.Errorf("sync record: %w", err)
	}
	if err := f.Close(); err != nil {
		return Entry{}, err
	}
	l.last, l.seq = e.Hash, e.Seq
	return e, nil
}

// Filter selects entries for Read. Zero fields match everything.
type Filter struct {
	Since   time.Time
	Until   time.Time
	Kind    string
	Job     string
	Text    string // case-insensitive match on DN, account, attribute, ticket or note
	Account string // requester, technician or approver
	Limit   int    // newest entries first when set
}

// Read returns entries from every writer under dir, oldest first, or the
// newest Limit entries when Limit is set.
func Read(dir string, f Filter) ([]Entry, error) {
	writers, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, w := range writers {
		if !w.IsDir() {
			continue
		}
		files, err := monthFiles(filepath.Join(dir, w.Name()))
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			entries, err := readFile(file)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if f.match(e) {
					out = append(out, e)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			if out[i].Writer == out[j].Writer {
				return out[i].Seq < out[j].Seq
			}
			return out[i].Writer < out[j].Writer
		}
		return out[i].Time.Before(out[j].Time)
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return out, nil
}

func (f Filter) match(e Entry) bool {
	if !f.Since.IsZero() && e.Time.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.Time.After(f.Until) {
		return false
	}
	if f.Kind != "" && e.Kind != f.Kind {
		return false
	}
	if f.Job != "" && e.Job != f.Job {
		return false
	}
	if f.Account != "" {
		a := strings.ToLower(f.Account)
		if !strings.Contains(strings.ToLower(e.Requester+"|"+e.Technician+"|"+e.Approver), a) {
			return false
		}
	}
	if f.Text != "" {
		t := strings.ToLower(f.Text)
		hay := strings.ToLower(strings.Join([]string{e.DN, e.Attribute, e.Ticket, e.Note, e.Requester, e.Technician, e.Change}, "|"))
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// Problem is one integrity failure found by Verify.
type Problem struct {
	Writer string `json:"writer"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// Verify checks every writer's chain under dir: each line's hash, its link to
// the previous line, sequence numbers, and that no month file is missing in
// the middle of a chain.
func Verify(dir string) ([]Problem, error) {
	writers, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var problems []Problem
	for _, w := range writers {
		if !w.IsDir() {
			continue
		}
		files, err := monthFiles(filepath.Join(dir, w.Name()))
		if err != nil {
			return nil, err
		}
		prev, seq := "", int64(0)
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
			for i, line := range lines {
				if len(line) == 0 {
					continue
				}
				p := Problem{Writer: w.Name(), File: filepath.Base(file), Line: i + 1}
				var e Entry
				if err := json.Unmarshal(line, &e); err != nil {
					p.Reason = "line is not a valid record"
					problems = append(problems, p)
					continue
				}
				want := e.Hash
				e.Hash = ""
				got, _ := hashEntry(e)
				switch {
				case got != want:
					p.Reason = "line was changed after it was written (hash mismatch)"
				case e.Prev != prev:
					p.Reason = "chain is broken here: a line before it is missing or was changed"
				case e.Seq != seq+1:
					p.Reason = fmt.Sprintf("sequence jumps from %d to %d", seq, e.Seq)
				case e.Writer != w.Name():
					p.Reason = "line belongs to a different writer"
				}
				if p.Reason != "" {
					problems = append(problems, p)
				}
				prev, seq = want, e.Seq
			}
		}
	}
	return problems, nil
}

func hashEntry(e Entry) (string, error) {
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func monthFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".jsonl") && len(n) == len("2006-01.jsonl") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out, nil
}

func readFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for s.Scan() {
		line := bytes.TrimSpace(s.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s: unreadable record line: %w", filepath.Base(path), err)
		}
		out = append(out, e)
	}
	return out, s.Err()
}

func cleanWriter(w string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(w) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
