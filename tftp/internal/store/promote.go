package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

// List reads validated committed metadata without reading configuration contents.
func List(directory string, limit int) ([]Receipt, error) {
	r, e := os.OpenRoot(directory)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	d := &Drop{root: r}
	out := []Receipt{}
	e = d.walkReceipts(func(v Receipt) error {
		out = append(out, v)
		if len(out) > limit {
			out = out[1:]
		}
		return nil
	})
	return out, e
}

// Promote copies a committed payload into the served root, verifies its receipt hash,
// and publishes without replacement. No metadata or partial payload is ever served.
func Promote(drop, served, storedAs, destination string) (string, error) {
	if e := Separate(served, drop); e != nil {
		return "", e
	}
	n, e := Name(destination)
	if e != nil {
		return "", e
	}
	src, e := os.OpenRoot(drop)
	if e != nil {
		return "", e
	}
	defer src.Close()
	sn, e := Name(storedAs)
	if e != nil {
		return "", e
	}
	// Only a receipt can authorize a promotion; arbitrary drop-box files cannot.
	parts := strings.SplitN(sn, "/", 5)
	if len(parts) != 5 || parts[3] != "payload" {
		return "", errors.New("not a collected payload")
	}
	receiptPath := strings.Join(parts[:4], "/")
	d := &Drop{root: src}
	rec, e := d.readReceipt(path.Dir(receiptPath) + "/receipt.json")
	if e != nil {
		return "", e
	}
	rn, e := Name(rec.Name)
	if e != nil || rec.StoredAs != sn || sn != receiptPath+"/"+rn {
		return "", errors.New("receipt mismatch")
	}
	from, e := src.OpenFile(sn, os.O_RDONLY|nonblock, 0)
	if e != nil {
		return "", e
	}
	defer from.Close()
	info, e := from.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return "", errors.New("not a regular payload")
	}
	dst, e := os.OpenRoot(served)
	if e != nil {
		return "", e
	}
	defer dst.Close()
	// A temporary directory outside the served root avoids exposing partial bytes.
	stage, e := os.MkdirTemp(pathParent(served), ".ztg-promote-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(stage)
	tmp, e := os.OpenFile(stage+string(os.PathSeparator)+"payload", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	count, e := io.Copy(io.MultiWriter(tmp, h), from)
	if e == nil {
		e = tmp.Sync()
	}
	ce := tmp.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return "", e
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if hash != rec.SHA256 || count != rec.Bytes {
		return "", errors.New("collected payload changed since receipt")
	}
	// Open the common parent as a Root so both sides of Link retain containment.
	// A served root aliased via symlinks is resolved before this operation.
	return hash, publishPromotion(stage, served, n)
}
