package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/findings"
)

var ErrTooLarge = errors.New("too-large")
var ErrSpace = errors.New("disk-space-floor")

const DefaultUploadMax int64 = 256 << 20
const DefaultFreeFloor uint64 = 1 << 30

type Receipt struct {
	Time         time.Time       `json:"time"`
	Source       string          `json:"source"`
	Name         string          `json:"name"`
	StoredAs     string          `json:"stored_as"`
	Bytes        int64           `json:"bytes"`
	SHA256       string          `json:"sha256"`
	Scan         findings.Result `json:"scan"`
	ChangedSince string          `json:"changed_since,omitempty"`
	Changed      *bool           `json:"changed,omitempty"`
}
type Drop struct {
	root      *os.Root
	directory *os.File
	lock      *os.File
	MaxBytes  int64
	FreeFloor uint64
	mu        sync.Mutex
	reserved  uint64
	space     func(*os.File) (uint64, error)
}
type Upload struct {
	drop              *Drop
	file              *os.File
	stage, final, key string
	receipt           Receipt
	remaining         uint64
	bytes             int64
	expected          int64
	committed, closed bool
}

// Separate resolves aliases before comparing ancestry. Containment of remote
// names is always enforced independently by os.Root.
func Separate(served, drop string) error {
	a, e := filepath.Abs(served)
	if e != nil {
		return e
	}
	a, e = filepath.EvalSymlinks(a)
	if e != nil {
		return e
	}
	b, e := filepath.Abs(drop)
	if e != nil {
		return e
	}
	b, e = filepath.EvalSymlinks(b)
	if e != nil {
		return e
	}
	inside := func(parent, child string) bool {
		r, e := filepath.Rel(parent, child)
		return e == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)))
	}
	if inside(a, b) || inside(b, a) {
		return errors.New("served root and drop box must be separate directory trees")
	}
	// SameFile catches aliases such as Windows case differences and Unix bind
	// mounts at either root. Walk existing ancestors to catch nested aliases too.
	infoA, e := os.Stat(a)
	if e != nil {
		return e
	}
	infoB, e := os.Stat(b)
	if e != nil {
		return e
	}
	for _, pair := range []struct {
		start  string
		target os.FileInfo
	}{{a, infoB}, {b, infoA}} {
		for current := pair.start; ; current = filepath.Dir(current) {
			info, e := os.Stat(current)
			if e != nil {
				return e
			}
			if os.SameFile(info, pair.target) {
				return errors.New("served root and drop box overlap through an alias")
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	return nil
}

func OpenDrop(directory, served string, maxBytes int64, floor uint64) (*Drop, error) {
	if maxBytes <= 0 {
		return nil, errors.New("upload cap must be positive")
	}
	if e := os.MkdirAll(directory, 0700); e != nil {
		return nil, e
	}
	if e := Separate(served, directory); e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(directory)
	if e != nil {
		return nil, e
	}
	f, e := r.Open(".")
	if e != nil {
		r.Close()
		return nil, e
	}
	d := &Drop{root: r, directory: f, MaxBytes: maxBytes, FreeFloor: floor, space: freeBytes}
	if e = private(f, true); e != nil {
		d.Close()
		return nil, e
	}
	d.lock, e = r.OpenFile(".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		d.Close()
		return nil, e
	}
	if e = private(d.lock, false); e != nil {
		d.Close()
		return nil, e
	}
	if e = lockDrop(d.lock); e != nil {
		d.Close()
		return nil, errors.New("drop box is already in use or cannot be locked")
	}
	// The exclusive process lock makes abandoned staging files safe to remove.
	if e = r.RemoveAll(".incoming"); e != nil {
		d.Close()
		return nil, e
	}
	for _, name := range []string{".incoming", ".index"} {
		if e = d.mkdir(name); e != nil {
			d.Close()
			return nil, e
		}
	}
	// A platform without reliable free-space accounting cannot accept uploads.
	if _, e = d.space(f); e != nil {
		d.Close()
		return nil, e
	}
	if e = d.recoverIndex(); e != nil {
		d.Close()
		return nil, e
	}
	return d, nil
}
func (d *Drop) Close() error {
	if d.lock != nil {
		d.lock.Close()
	}
	d.directory.Close()
	return d.root.Close()
}
func (d *Drop) mkdir(name string) error {
	if e := d.root.Mkdir(name, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return e
	}
	f, e := d.root.OpenFile(name, os.O_RDONLY|nonblock, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return errors.New("drop box component is not a directory")
	}
	return private(f, true)
}
func (d *Drop) mkdirAll(name string) error {
	current := ""
	for _, part := range strings.Split(name, "/") {
		current = path.Join(current, part)
		if e := d.mkdir(current); e != nil {
			return e
		}
	}
	return nil
}
func randomID() string { var b [16]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }

func (d *Drop) Begin(source netip.Addr, name string, size int64) (*Upload, error) {
	n, e := Name(name)
	if e != nil {
		return nil, e
	}
	if !source.IsValid() {
		return nil, errors.New("invalid source")
	}
	source = source.Unmap().WithZone("")
	if size > d.MaxBytes {
		return nil, ErrTooLarge
	}
	reserve := d.MaxBytes
	if size >= 0 {
		reserve = size
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	free, e := d.space(d.directory)
	if e != nil {
		return nil, e
	}
	if free < d.FreeFloor || d.reserved > free-d.FreeFloor || uint64(reserve) > free-d.FreeFloor-d.reserved {
		return nil, ErrSpace
	}
	id := randomID()
	stage := ".incoming/" + id
	if e = d.root.Mkdir(stage, 0700); e != nil {
		return nil, e
	}
	cleanup := true
	defer func() {
		if cleanup {
			d.root.RemoveAll(stage)
		}
	}()
	if e = d.mkdir(stage); e != nil {
		return nil, e
	}
	if e = d.mkdirAll(stage + "/payload/" + path.Dir(n)); e != nil {
		return nil, e
	}
	f, e := d.root.OpenFile(stage+"/payload/"+n, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = private(f, false); e != nil {
		f.Close()
		return nil, e
	}
	now := time.Now().UTC()
	ip := strings.ReplaceAll(source.String(), ":", "_")
	final := now.Format("2006-01-02") + "/" + ip + "/" + now.Format("150405") + "-" + id
	key := sha256.Sum256([]byte(source.String() + "\x00" + n))
	d.reserved += uint64(reserve)
	cleanup = false
	return &Upload{drop: d, file: f, stage: stage, final: final, key: hex.EncodeToString(key[:]), remaining: uint64(reserve), expected: size, receipt: Receipt{Time: now, Source: source.String(), Name: n, StoredAs: final + "/payload/" + n}}, nil
}
func (u *Upload) Write(b []byte) (int, error) {
	if u.closed {
		return 0, os.ErrClosed
	}
	if int64(len(b)) > u.drop.MaxBytes-u.bytes || u.expected >= 0 && int64(len(b)) > u.expected-u.bytes {
		return 0, ErrTooLarge
	}
	d := u.drop
	d.mu.Lock()
	defer d.mu.Unlock()
	free, e := d.space(d.directory)
	if e != nil {
		return 0, e
	}
	if free < d.FreeFloor || uint64(len(b)) > free-d.FreeFloor {
		return 0, ErrSpace
	}
	n, e := u.file.Write(b)
	u.bytes += int64(n)
	u.remaining -= uint64(n)
	d.reserved -= uint64(n)
	return n, e
}
func (d *Drop) readReceipt(name string) (Receipt, error) {
	var receipt Receipt
	f, e := d.root.OpenFile(name, os.O_RDONLY|nonblock, 0)
	if e != nil {
		return receipt, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return receipt, errors.New("invalid receipt")
	}
	e = json.NewDecoder(io.LimitReader(f, 256<<10)).Decode(&receipt)
	if e != nil {
		return receipt, errors.New("invalid receipt")
	}
	return receipt, nil
}
func (d *Drop) writeJSON(name string, value any) error {
	f, e := d.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if e = private(f, false); e == nil {
		e = json.NewEncoder(f).Encode(value)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}

// Commit runs before the final ACK. A successful ACK therefore means the file
// and its receipt are finished, named, and available for the operator to inspect.
func (u *Upload) Commit(bytes int64, hash string) (Receipt, error) {
	if u.closed {
		return Receipt{}, os.ErrClosed
	}
	if bytes != u.bytes || u.expected >= 0 && u.bytes != u.expected {
		return Receipt{}, errors.New("transfer-size-mismatch")
	}
	if e := u.file.Sync(); e != nil {
		return Receipt{}, e
	}
	if _, e := u.file.Seek(0, io.SeekStart); e != nil {
		return Receipt{}, e
	}
	scan, e := findings.Scan(u.file)
	if e != nil {
		return Receipt{}, e
	}
	u.receipt.Bytes, u.receipt.SHA256, u.receipt.Scan = u.bytes, hash, scan
	d := u.drop
	d.mu.Lock()
	defer d.mu.Unlock()
	u.receipt.Time = time.Now().UTC()
	index := ".index/" + u.key + ".json"
	previous, e := d.readReceipt(index)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return Receipt{}, e
	}
	if e == nil {
		changed := previous.SHA256 != hash
		u.receipt.Changed = &changed
		u.receipt.ChangedSince = previous.StoredAs
	}
	if e = d.mkdirAll(path.Dir(u.final)); e != nil {
		return Receipt{}, e
	}
	// Reserve a unique destination directory exclusively. Payloads use Link,
	// whose no-replace semantics are the same on Unix and NTFS.
	if e = d.root.Mkdir(u.final, 0700); e != nil {
		return Receipt{}, e
	}
	committed := false
	defer func() {
		if !committed {
			d.root.RemoveAll(u.final)
		}
	}()
	if e = d.mkdir(u.final); e != nil {
		return Receipt{}, e
	}
	if e = d.mkdirAll(path.Dir(u.receipt.StoredAs)); e != nil {
		return Receipt{}, e
	}
	if e = d.writeJSON(u.final+"/receipt.json", u.receipt); e != nil {
		return Receipt{}, e
	}
	// Windows requires the writing handle to close before publishing a hard link.
	if e = u.file.Close(); e != nil {
		return Receipt{}, e
	}
	if e = d.root.Link(u.stage+"/payload/"+u.receipt.Name, u.receipt.StoredAs); e != nil {
		return Receipt{}, e
	}
	tmpIndex := ".index/" + u.key + "-" + randomID() + ".tmp"
	defer d.root.Remove(tmpIndex)
	if e = d.writeJSON(tmpIndex, u.receipt); e != nil {
		return Receipt{}, e
	}
	if e = d.root.Rename(tmpIndex, index); e != nil {
		return Receipt{}, e
	}
	committed = true
	u.committed = true
	d.reserved -= u.remaining
	u.remaining = 0
	d.root.RemoveAll(u.stage)
	return u.receipt, nil
}
func (u *Upload) Close() error {
	if u.closed {
		return nil
	}
	u.closed = true
	u.file.Close()
	u.drop.mu.Lock()
	u.drop.reserved -= u.remaining
	u.remaining = 0
	u.drop.mu.Unlock()
	if !u.committed {
		return u.drop.root.RemoveAll(u.stage)
	}
	return nil
}

func (d *Drop) recoverIndex() error {
	// The process can stop between payload publication and index replacement.
	// Rebuild from committed receipts under the exclusive lock at startup.
	if e := d.root.RemoveAll(".index"); e != nil {
		return e
	}
	if e := d.mkdir(".index"); e != nil {
		return e
	}
	return d.walkReceipts(func(receipt Receipt) error {
		key := sha256.Sum256([]byte(receipt.Source + "\x00" + receipt.Name))
		name := ".index/" + hex.EncodeToString(key[:]) + ".json"
		old, e := d.readReceipt(name)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e == nil && (old.Time.After(receipt.Time) || old.Time.Equal(receipt.Time) && old.StoredAs >= receipt.StoredAs) {
			return nil
		}
		tmp := name + "-" + randomID()
		defer d.root.Remove(tmp)
		if e = d.writeJSON(tmp, receipt); e != nil {
			return e
		}
		return d.root.Rename(tmp, name)
	})
}

func (d *Drop) walkReceipts(emit func(Receipt) error) error {
	return fs.WalkDir(d.root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".incoming" || entry.Name() == ".index" || entry.Name() == "payload") {
			return fs.SkipDir
		}
		if entry.IsDir() || entry.Name() != "receipt.json" || len(strings.Split(name, "/")) != 4 {
			return nil
		}
		receipt, e := d.readReceipt(name)
		if e != nil {
			return e
		}
		normalized, e := Name(receipt.Name)
		if e != nil {
			return e
		}
		if receipt.StoredAs != path.Dir(name)+"/payload/"+normalized {
			return errors.New("receipt does not match collected path")
		}
		if _, e = netip.ParseAddr(receipt.Source); e != nil {
			return errors.New("invalid receipt source")
		}
		info, e := d.root.Stat(receipt.StoredAs)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("invalid collected file")
		}
		return emit(receipt)
	})
}

// Rescan visits committed receipts and payloads only, never staging files or the
// mutable history index. All opens and traversal remain under a runtime Root.
func Rescan(directory string, emit func(Receipt) error) error {
	r, e := os.OpenRoot(directory)
	if e != nil {
		return e
	}
	defer r.Close()
	d := &Drop{root: r}
	return d.walkReceipts(func(receipt Receipt) error {
		n, e := Name(receipt.StoredAs)
		if e != nil {
			return e
		}
		f, e := r.OpenFile(n, os.O_RDONLY|nonblock, 0)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			f.Close()
			return errors.New("invalid collected file")
		}
		receipt.Scan, e = findings.Scan(f)
		f.Close()
		if e != nil {
			return e
		}
		return emit(receipt)
	})
}
